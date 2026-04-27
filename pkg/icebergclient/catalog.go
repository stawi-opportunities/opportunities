// Package icebergclient provides shared helpers for opening the
// cluster's Iceberg REST catalog (Lakekeeper).
//
// This is the canonical home for catalog construction logic. Both the
// writer service and candidatestore import from here; apps/writer/service
// re-exports the symbols for back-compat.
//
// Lakekeeper owns metadata storage (its own Postgres) and storage-side
// credentials (R2 access keys configured per-warehouse). Apps no longer
// hold catalog DB credentials. R2 access for direct parquet writes
// (eventlog uploads, KV rebuild) is still configured per-app via R2_*
// env vars; that path is independent of catalog operations.
package icebergclient

import (
	"context"

	"github.com/apache/iceberg-go/catalog"
	restcat "github.com/apache/iceberg-go/catalog/rest"
)

// CatalogConfig carries everything needed to open the shared cluster
// Iceberg REST catalog (Lakekeeper).
type CatalogConfig struct {
	// Name is the local catalog handle (e.g. "stawi"). Used by
	// iceberg-go for logs and identification — not the warehouse name.
	Name string

	// URI is the Lakekeeper REST endpoint, e.g.
	// http://lakekeeper-catalog.lakehouse.svc.cluster.local:8181/catalog
	URI string

	// Warehouse is the logical warehouse name registered in Lakekeeper
	// (e.g. "product-opportunities"). The catalog client passes this on
	// every request so Lakekeeper knows which storage backend to use.
	Warehouse string

	// OAuthToken is an optional pre-obtained bearer token. Empty string
	// disables auth on requests (matches Lakekeeper running with
	// LAKEKEEPER__OPENID_PROVIDER_URI unset).
	OAuthToken string

	// Credential is an optional "client_id:client_secret" pair for the
	// OAuth2 client_credentials flow. Empty string disables. Reserved
	// for the future when cluster Lakekeeper enables OIDC.
	Credential string
}

// LoadCatalog opens the shared Lakekeeper REST catalog. The returned
// catalog.Catalog is safe for concurrent use across goroutines; callers
// should construct it once at startup and share it.
//
// The returned catalog talks REST to Lakekeeper. Lakekeeper handles the
// underlying metadata storage and the data storage (R2 via vended
// credentials or pre-configured per-warehouse access). The Go binary
// doesn't need R2 credentials for catalog operations — data-plane R2
// access is still configured per-app for parquet writes.
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
