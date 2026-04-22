package candidatestore

// catalog.go — thin re-export of icebergclient.LoadCatalog so that
// callers who only import candidatestore can construct a catalog without
// also importing pkg/icebergclient directly.

import (
	"context"

	"github.com/apache/iceberg-go/catalog"

	"stawi.jobs/pkg/icebergclient"
)

// CatalogConfig is an alias for icebergclient.CatalogConfig.
// Callers may use either type; they are structurally identical.
type CatalogConfig = icebergclient.CatalogConfig

// LoadCatalog opens a named SQL-backed Iceberg catalog.
// It is a thin wrapper over icebergclient.LoadCatalog.
func LoadCatalog(ctx context.Context, cfg CatalogConfig) (catalog.Catalog, error) {
	return icebergclient.LoadCatalog(ctx, cfg)
}
