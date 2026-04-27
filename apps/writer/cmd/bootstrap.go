// bootstrap.go — implementation of the `writer bootstrap-iceberg`
// subcommand. Idempotent: registers the Lakekeeper warehouse, creates
// the `opportunities` and `candidates` namespaces, then materialises
// the canonical 12-table Iceberg schema.
//
// Designed to run as a Kubernetes Job on every FluxCD reconcile —
// duplicate bootstrap runs are safe (every step short-circuits on
// "already exists").

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/apache/iceberg-go/catalog"
	"github.com/pitabwire/util"

	writercfg "github.com/stawi-opportunities/opportunities/apps/writer/config"
	"github.com/stawi-opportunities/opportunities/pkg/icebergclient"
)

// readinessTimeout caps how long bootstrap will poll
// /management/v1/info before giving up. Lakekeeper boots in <30 s in
// the cluster, but DB migrations on a fresh deploy push it longer.
const (
	readinessTimeout  = 5 * time.Minute
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

	mgmtBase := managementBase(cfg.IcebergCatalogURI)
	httpClient := &http.Client{Timeout: 30 * time.Second}

	log.WithField("base", mgmtBase).Info("bootstrap: waiting for Lakekeeper readiness")
	if err := waitForLakekeeper(ctx, httpClient, mgmtBase); err != nil {
		return fmt.Errorf("lakekeeper readiness: %w", err)
	}

	// Lakekeeper requires accept-terms once per cluster before any
	// management call works. Idempotent — returns 200 on subsequent
	// invocations (or 4xx that we treat as already-bootstrapped).
	if err := acceptBootstrapTerms(ctx, httpClient, mgmtBase); err != nil {
		log.WithError(err).Warn("bootstrap: accept-terms call non-fatal failure (likely already bootstrapped)")
	}

	if err := ensureWarehouse(ctx, httpClient, mgmtBase, cfg); err != nil {
		return fmt.Errorf("ensure warehouse: %w", err)
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

// managementBase strips the trailing /catalog path from
// ICEBERG_CATALOG_URI to produce the management-API base URL.
// Example:
//
//	"http://lakekeeper-catalog.lakehouse.svc.cluster.local:8181/catalog"
//	→ "http://lakekeeper-catalog.lakehouse.svc.cluster.local:8181"
func managementBase(catalogURI string) string {
	trimmed := strings.TrimRight(catalogURI, "/")
	if strings.HasSuffix(trimmed, "/catalog") {
		return strings.TrimSuffix(trimmed, "/catalog")
	}
	return trimmed
}

// waitForLakekeeper polls /management/v1/info until 2xx or the deadline
// expires. Returns nil when the catalog is reachable.
func waitForLakekeeper(ctx context.Context, client *http.Client, base string) error {
	deadline := time.Now().Add(readinessTimeout)
	url := base + "/management/v1/info"
	log := util.Log(ctx)

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				log.WithField("status", resp.StatusCode).Info("bootstrap: Lakekeeper ready")
				return nil
			}
			log.WithField("status", resp.StatusCode).Debug("bootstrap: Lakekeeper not yet ready")
		} else {
			log.WithError(err).Debug("bootstrap: Lakekeeper info probe failed")
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("Lakekeeper not ready after %s (last err: %v)", readinessTimeout, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(readinessInterval):
		}
	}
}

// acceptBootstrapTerms posts to /management/v1/bootstrap. Subsequent
// calls return non-200 which we tolerate — Lakekeeper has no GET to
// inspect bootstrap state, but the body of management/v1/info indicates
// it. We swallow non-success here because the warehouse step will fail
// loudly if bootstrap was the actual blocker.
func acceptBootstrapTerms(ctx context.Context, client *http.Client, base string) error {
	body := []byte(`{"accept-terms-of-use": true}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/management/v1/bootstrap", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		util.Log(ctx).Info("bootstrap: accept-terms accepted")
		return nil
	}
	// Conflict / already-bootstrapped is fine; surface as nil to caller.
	if resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusBadRequest {
		util.Log(ctx).WithField("status", resp.StatusCode).Debug("bootstrap: accept-terms already done")
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return fmt.Errorf("accept-terms: unexpected status %d: %s", resp.StatusCode, string(b))
}

// warehouseListResponse models the "warehouses" key in the response of
// GET /management/v1/warehouse. Lakekeeper 0.10.x returns each item as
// an object with a "name" field; we only inspect that.
type warehouseListResponse struct {
	Warehouses []struct {
		Name string `json:"name"`
	} `json:"warehouses"`
}

// ensureWarehouse lists warehouses; if cfg.IcebergWarehouse is missing,
// POSTs the registration body. The body shape is the Lakekeeper 0.10.x
// shape (verified against deployment.manifests/MIGRATIONS.md).
func ensureWarehouse(ctx context.Context, client *http.Client, base string, cfg writercfg.Config) error {
	log := util.Log(ctx).WithField("warehouse", cfg.IcebergWarehouse)

	listReq, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/management/v1/warehouse", nil)
	if err != nil {
		return err
	}
	listResp, err := client.Do(listReq)
	if err != nil {
		return fmt.Errorf("list warehouses: %w", err)
	}
	defer listResp.Body.Close()
	if listResp.StatusCode < 200 || listResp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(listResp.Body, 1024))
		return fmt.Errorf("list warehouses: status %d: %s", listResp.StatusCode, string(b))
	}
	var listed warehouseListResponse
	if err := json.NewDecoder(listResp.Body).Decode(&listed); err != nil {
		return fmt.Errorf("decode warehouse list: %w", err)
	}
	for _, w := range listed.Warehouses {
		if w.Name == cfg.IcebergWarehouse {
			log.Info("bootstrap: warehouse already registered")
			return nil
		}
	}

	body := map[string]any{
		"warehouse-name": cfg.IcebergWarehouse,
		"delete-profile": map[string]string{"type": "hard"},
		"storage-credential": map[string]string{
			"credential-type":   "cloudflare-r2",
			"account-id":        cfg.R2AccountID,
			"access-key-id":     cfg.R2AccessKeyID,
			"secret-access-key": cfg.R2SecretAccessKey,
			"token":             "",
		},
		"storage-profile": map[string]any{
			"type":       "s3",
			"bucket":     cfg.R2Bucket,
			"region":     cfg.R2Region,
			"key-prefix": cfg.IcebergWarehouse + "/",
			"endpoint":   cfg.R2Endpoint,
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}

	postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/management/v1/warehouse", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	postReq.Header.Set("Content-Type", "application/json")
	postResp, err := client.Do(postReq)
	if err != nil {
		return fmt.Errorf("post warehouse: %w", err)
	}
	defer postResp.Body.Close()
	switch {
	case postResp.StatusCode >= 200 && postResp.StatusCode < 300:
		log.Info("bootstrap: warehouse registered")
		return nil
	case postResp.StatusCode == http.StatusConflict:
		// Race with another bootstrap pod; treat as success.
		log.Info("bootstrap: warehouse already registered (409)")
		return nil
	default:
		b, _ := io.ReadAll(io.LimitReader(postResp.Body, 4096))
		return fmt.Errorf("register warehouse: status %d: %s", postResp.StatusCode, string(b))
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
			// Some catalog implementations don't wrap their "already exists"
			// errors with errors.Is — defensively match the message too.
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
// "Already exists" errors are tolerated.
func ensureTables(ctx context.Context, cat catalog.Catalog) error {
	log := util.Log(ctx)
	for _, ts := range icebergclient.AllTableSchemas() {
		_, err := cat.CreateTable(ctx, ts.Identifier, ts.Schema())
		switch {
		case err == nil:
			log.WithField("table", ts.Identifier).Info("bootstrap: table created")
		case errors.Is(err, catalog.ErrTableAlreadyExists):
			log.WithField("table", ts.Identifier).Debug("bootstrap: table already exists")
		default:
			if isAlreadyExistsErr(err) {
				log.WithField("table", ts.Identifier).Debug("bootstrap: table already exists (string match)")
				continue
			}
			return fmt.Errorf("create table %v: %w", ts.Identifier, err)
		}
	}
	return nil
}

// isAlreadyExistsErr is a defensive fallback for catalog backends whose
// error wrapping doesn't satisfy errors.Is on the canonical sentinels.
// REST + Glue both return text containing the phrase; we match it
// case-insensitively.
func isAlreadyExistsErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already exists") || strings.Contains(msg, "alreadyexists")
}

