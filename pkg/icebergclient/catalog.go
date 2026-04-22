// Package icebergclient provides shared helpers for opening Iceberg SQL
// catalogs backed by Postgres + an S3-compatible object store (R2).
//
// This is the canonical home for catalog construction logic. Both the
// writer service and candidatestore import from here; apps/writer/service
// re-exports the symbols for back-compat.
package icebergclient

import (
	"context"

	iceberg "github.com/apache/iceberg-go"
	"github.com/apache/iceberg-go/catalog"
	_ "github.com/apache/iceberg-go/catalog/sql" // register "sql" catalog driver
)

// CatalogConfig carries all parameters needed to open an Iceberg SQL catalog.
type CatalogConfig struct {
	// Name is the catalog registration name (e.g. "stawi").
	Name string
	// URI is the Postgres DSN used by the SQL catalog backend.
	URI string
	// Warehouse is the S3 URI prefix for Iceberg metadata
	// (e.g. "s3://stawi-jobs-log/iceberg").
	Warehouse string

	// R2 / S3-compatible credentials.
	R2Endpoint        string
	R2AccessKeyID     string
	R2SecretAccessKey string
	R2Region          string
}

// LoadCatalog opens a named SQL catalog backed by Postgres + R2.
// The returned catalog.Catalog is safe for concurrent use across
// goroutines; callers should construct it once at startup and share it.
func LoadCatalog(ctx context.Context, cfg CatalogConfig) (catalog.Catalog, error) {
	props := iceberg.Properties{
		"type":                  "sql",
		"uri":                   cfg.URI,
		"sql.dialect":           "postgres",
		"warehouse":             cfg.Warehouse,
		"s3.endpoint":           cfg.R2Endpoint,
		"s3.access-key-id":      cfg.R2AccessKeyID,
		"s3.secret-access-key":  cfg.R2SecretAccessKey,
		"s3.region":             cfg.R2Region,
		"s3.path-style-access":  "true",
	}
	return catalog.Load(ctx, cfg.Name, props)
}
