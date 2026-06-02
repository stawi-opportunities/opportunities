// Package icebergclient provides shared helpers for opening the
// cluster's Iceberg REST catalog.
//
// This is the canonical home for catalog construction logic. Both the
// writer service and candidatestore import from here; apps/writer/service
// re-exports the symbols for back-compat.
//
// The catalog backend is Cloudflare R2 Data Catalog — a managed Iceberg
// REST catalog built into the R2 bucket. Apps authenticate with a
// Cloudflare API token (bearer). R2 access for direct parquet writes
// (eventlog uploads, KV rebuild) is still configured per-app via R2_*
// env vars; that path is independent of catalog operations.
package icebergclient

import (
	"context"

	"github.com/apache/iceberg-go/catalog"
	restcat "github.com/apache/iceberg-go/catalog/rest"
)

// CatalogConfig carries everything needed to open the shared cluster
// Iceberg REST catalog.
type CatalogConfig struct {
	// Name is the local catalog handle (e.g. "stawi"). Used by
	// iceberg-go for logs and identification — not the warehouse name.
	Name string

	// URI is the Iceberg REST catalog endpoint. For R2 Data Catalog
	// this is the catalog URI returned by `wrangler r2 bucket catalog enable`.
	URI string

	// Warehouse is the logical warehouse name. For R2 Data Catalog
	// this is returned alongside the catalog URI when the catalog is
	// enabled on a bucket.
	Warehouse string

	// OAuthToken is a bearer token for catalog API authentication.
	// For R2 Data Catalog this is a Cloudflare API token with both
	// R2 Data Catalog and R2 Storage permissions.
	OAuthToken string

	// Credential is an optional "client_id:client_secret" pair for the
	// OAuth2 client_credentials flow. Empty string disables.
	Credential string
}

// LoadCatalog opens the Iceberg REST catalog. The returned
// catalog.Catalog is safe for concurrent use across goroutines; callers
// should construct it once at startup and share it.
func LoadCatalog(ctx context.Context, cfg CatalogConfig) (catalog.Catalog, error) {
	opts := []restcat.Option{}
	if cfg.Warehouse != "" {
		opts = append(opts, restcat.WithWarehouseLocation(cfg.Warehouse))
	}
	if cfg.OAuthToken != "" {
		opts = append(opts, restcat.WithOAuthToken(cfg.OAuthToken))
	}
	if cfg.Credential != "" {
		opts = append(opts, restcat.WithCredential(cfg.Credential))
	}
	return restcat.NewCatalog(ctx, cfg.Name, cfg.URI, opts...)
}

// Ensure catalog.Catalog is still satisfied — compile-time guard.
var _ catalog.Catalog = (catalog.Catalog)(nil)
