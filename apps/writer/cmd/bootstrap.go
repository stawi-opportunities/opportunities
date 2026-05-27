// bootstrap.go — implementation of the `writer bootstrap-iceberg`
// subcommand. Idempotent: enables R2 Data Catalog on the chronicle
// bucket (if not already enabled), then creates the `opportunities`
// and `candidates` namespaces and materialises the canonical Iceberg
// table schema.
//
// Designed to run as a Kubernetes Job on every FluxCD reconcile —
// duplicate bootstrap runs are safe (every step short-circuits on
// "already exists").

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/apache/iceberg-go/catalog"
	frameclient "github.com/pitabwire/frame/client"
	"github.com/pitabwire/util"

	writercfg "github.com/stawi-opportunities/opportunities/apps/writer/config"
	"github.com/stawi-opportunities/opportunities/pkg/icebergclient"
)

// readinessTimeout caps how long bootstrap will poll the R2 Data Catalog
// API before giving up.
const (
	readinessTimeout  = 3 * time.Minute
	readinessInterval = 5 * time.Second
)

// runBootstrap is the entry point invoked from main when the binary is
// launched as `writer bootstrap-iceberg`. It exits the process via the
// caller (main) on success or failure; failures bubble up as an error
// so main can log + exit non-zero.
func runBootstrap(ctx context.Context) error {
	cfg, err := writercfg.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log := util.Log(ctx)

	httpClient := frameclient.NewHTTPClient(ctx,
		frameclient.WithHTTPTimeout(30*time.Second),
		frameclient.WithHTTPTraceRequests(),
	)

	// Enable R2 Data Catalog on the chronicle bucket (idempotent).
	log.WithField("bucket", cfg.R2Bucket).Info("bootstrap: ensuring R2 Data Catalog is enabled")
	if err := ensureR2Catalog(ctx, httpClient, cfg); err != nil {
		return fmt.Errorf("enable R2 catalog: %w", err)
	}

	// Wait for the catalog REST endpoint to be reachable before
	// creating namespaces and tables.
	log.WithField("bucket", cfg.R2Bucket).Info("bootstrap: waiting for catalog to be active")
	if err := waitForCatalog(ctx, httpClient, cfg); err != nil {
		return fmt.Errorf("catalog readiness: %w", err)
	}

	cat, err := icebergclient.LoadCatalog(ctx, icebergclient.CatalogConfig{
		Name:       cfg.IcebergCatalogName,
		URI:        cfg.IcebergCatalogURI,
		Warehouse:  cfg.IcebergWarehouse,
		OAuthToken: cfg.IcebergCatalogToken,
	})
	if err != nil {
		return fmt.Errorf("open catalog: %w", err)
	}

	if err := ensureNamespaces(ctx, cat); err != nil {
		return fmt.Errorf("ensure namespaces: %w", err)
	}

	if err := ensureTables(ctx, cat); err != nil {
		return fmt.Errorf("ensure tables: %w", err)
	}

	log.Info("bootstrap-iceberg: complete")
	return nil
}

// ensureR2Catalog enables the R2 Data Catalog on the chronicle bucket
// via the Cloudflare API. Idempotent — if already enabled, the API
// returns the existing catalog details.
func ensureR2Catalog(ctx context.Context, client *http.Client, cfg writercfg.Config) error {
	log := util.Log(ctx).WithField("bucket", cfg.R2Bucket)

	// Check if catalog is already enabled.
	statusURL := fmt.Sprintf(
		"https://api.cloudflare.com/client/v4/accounts/%s/r2-catalog/%s",
		cfg.R2AccountID, cfg.R2Bucket,
	)
	statusReq, err := http.NewRequestWithContext(ctx, http.MethodGet, statusURL, nil)
	if err != nil {
		return err
	}
	statusReq.Header.Set("Authorization", "Bearer "+cfg.CloudflareAPIToken)

	statusResp, err := client.Do(statusReq)
	if err != nil {
		return fmt.Errorf("check catalog status: %w", err)
	}
	defer func() { _ = statusResp.Body.Close() }()

	if statusResp.StatusCode == http.StatusOK {
		var body struct {
			Result struct {
				Status string `json:"status"`
			} `json:"result"`
		}
		if err := json.NewDecoder(statusResp.Body).Decode(&body); err == nil && body.Result.Status == "active" {
			log.Info("bootstrap: R2 Data Catalog already enabled")
			return nil
		}
	}

	// Enable the catalog.
	enableURL := fmt.Sprintf(
		"https://api.cloudflare.com/client/v4/accounts/%s/r2-catalog/%s/enable",
		cfg.R2AccountID, cfg.R2Bucket,
	)
	enableReq, err := http.NewRequestWithContext(ctx, http.MethodPost, enableURL, nil)
	if err != nil {
		return err
	}
	enableReq.Header.Set("Authorization", "Bearer "+cfg.CloudflareAPIToken)
	enableReq.Header.Set("Content-Type", "application/json")

	enableResp, err := client.Do(enableReq)
	if err != nil {
		return fmt.Errorf("enable catalog: %w", err)
	}
	defer func() { _ = enableResp.Body.Close() }()

	if enableResp.StatusCode >= 200 && enableResp.StatusCode < 300 {
		log.Info("bootstrap: R2 Data Catalog enabled")
		return nil
	}
	if enableResp.StatusCode == http.StatusConflict {
		log.Info("bootstrap: R2 Data Catalog already enabled (409)")
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(enableResp.Body, 4096))
	return fmt.Errorf("enable catalog: status %d: %s", enableResp.StatusCode, string(b))
}

// waitForCatalog polls the Cloudflare R2 Data Catalog management API
// until the catalog status is "active" or the deadline expires.
func waitForCatalog(ctx context.Context, client *http.Client, cfg writercfg.Config) error {
	deadline := time.Now().Add(readinessTimeout)
	url := fmt.Sprintf(
		"https://api.cloudflare.com/client/v4/accounts/%s/r2-catalog/%s",
		cfg.R2AccountID, cfg.R2Bucket,
	)
	log := util.Log(ctx)

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+cfg.CloudflareAPIToken)
		resp, err := client.Do(req)
		if err == nil {
			var body struct {
				Result struct {
					Status string `json:"status"`
				} `json:"result"`
			}
			if decErr := json.NewDecoder(resp.Body).Decode(&body); decErr == nil && body.Result.Status == "active" {
				_ = resp.Body.Close()
				log.Info("bootstrap: catalog active")
				return nil
			}
			_ = resp.Body.Close()
			log.WithField("status", resp.StatusCode).Debug("bootstrap: catalog not yet active")
		} else {
			log.WithError(err).Debug("bootstrap: catalog probe failed")
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("catalog not active after %s (last err: %v)", readinessTimeout, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(readinessInterval):
		}
	}
}

// ensureNamespaces creates each namespace returned by
// BootstrapNamespaces. "Already exists" is silently skipped.
func ensureNamespaces(ctx context.Context, cat catalog.Catalog) error {
	log := util.Log(ctx)
	for _, ns := range icebergclient.BootstrapNamespaces() {
		err := cat.CreateNamespace(ctx, ns, nil)
		switch {
		case err == nil:
			log.WithField("namespace", ns).Info("bootstrap: namespace created")
		case errors.Is(err, catalog.ErrNamespaceAlreadyExists):
			log.WithField("namespace", ns).Debug("bootstrap: namespace already exists")
		default:
			if isAlreadyExistsErr(err) {
				log.WithField("namespace", ns).Debug("bootstrap: namespace already exists (string match)")
				continue
			}
			return fmt.Errorf("create namespace %v: %w", ns, err)
		}
	}
	return nil
}

// ensureTables iterates AllTableSchemas and CreateTable's each entry.
// "Already exists" errors are tolerated. A 2-second pause between calls
// avoids tripping R2 Data Catalog's write rate limit (~10 writes/min).
func ensureTables(ctx context.Context, cat catalog.Catalog) error {
	log := util.Log(ctx)
	schemas := icebergclient.AllTableSchemas()
	for i, ts := range schemas {
		_, err := cat.CreateTable(ctx, ts.Identifier, ts.Schema())
		switch {
		case err == nil:
			log.WithField("table", ts.Identifier).Info("bootstrap: table created")
		case errors.Is(err, catalog.ErrTableAlreadyExists):
			log.WithField("table", ts.Identifier).Debug("bootstrap: table already exists")
		default:
			if isAlreadyExistsErr(err) {
				log.WithField("table", ts.Identifier).Debug("bootstrap: table already exists (string match)")
			} else if isRateLimitErr(err) {
				log.WithField("table", ts.Identifier).Warn("bootstrap: rate limited, backing off 30s")
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(30 * time.Second):
				}
				// Retry once after backoff.
				_, retryErr := cat.CreateTable(ctx, ts.Identifier, ts.Schema())
				if retryErr != nil && !isAlreadyExistsErr(retryErr) && !errors.Is(retryErr, catalog.ErrTableAlreadyExists) {
					return fmt.Errorf("create table %v (after retry): %w", ts.Identifier, retryErr)
				}
				log.WithField("table", ts.Identifier).Info("bootstrap: table created (after retry)")
			} else {
				return fmt.Errorf("create table %v: %w", ts.Identifier, err)
			}
		}
		if i < len(schemas)-1 {
			time.Sleep(2 * time.Second)
		}
	}
	return nil
}

func isRateLimitErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "rate limit") || strings.Contains(msg, "toomanyrequests") || strings.Contains(msg, "429")
}

// isAlreadyExistsErr is a defensive fallback for catalog backends whose
// error wrapping doesn't satisfy errors.Is on the canonical sentinels.
func isAlreadyExistsErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already exists") || strings.Contains(msg, "alreadyexists")
}
