// Package icebergclient provides shared helpers for opening Iceberg SQL
// catalogs backed by Postgres + an S3-compatible object store (R2).
//
// This is the canonical home for catalog construction logic. Both the
// writer service and candidatestore import from here; apps/writer/service
// re-exports the symbols for back-compat.
package icebergclient

import (
	"context"
	"database/sql"
	"time"

	iceberg "github.com/apache/iceberg-go"
	"github.com/apache/iceberg-go/catalog"
	sqlcat "github.com/apache/iceberg-go/catalog/sql"
	_ "github.com/jackc/pgx/v5/stdlib" // register "pgx" database/sql driver
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

	// Connection pool sizing for the Postgres catalog backend.
	//
	// With 6 services × 2+ replicas × concurrent catalog commits, the default
	// unlimited connection pool can exhaust Postgres max_connections during
	// compaction bursts. Tune these to stay within the cluster's connection
	// budget (typically 100–200 total across all services).
	//
	// Defaults: MaxOpenConns=10, MaxIdleConns=5.
	// Expose as ICEBERG_CATALOG_MAX_OPEN_CONNS / ICEBERG_CATALOG_MAX_IDLE_CONNS
	// environment variables in each service's config loader.
	MaxOpenConns int // default 10
	MaxIdleConns int // default 5
}

// LoadCatalog opens a named SQL catalog backed by Postgres + R2.
// The returned catalog.Catalog is safe for concurrent use across
// goroutines; callers should construct it once at startup and share it.
//
// Connection pool: MaxOpenConns and MaxIdleConns from cfg are applied to
// the underlying *sql.DB before handing it to the catalog constructor.
// This prevents unconstrained connection growth under compaction bursts.
func LoadCatalog(ctx context.Context, cfg CatalogConfig) (catalog.Catalog, error) {
	maxOpen := cfg.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = 10
	}
	maxIdle := cfg.MaxIdleConns
	if maxIdle <= 0 {
		maxIdle = 5
	}

	db, err := sql.Open("pgx", cfg.URI)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(30 * time.Minute)

	props := iceberg.Properties{
		"warehouse":            cfg.Warehouse,
		"s3.endpoint":          cfg.R2Endpoint,
		"s3.access-key-id":     cfg.R2AccessKeyID,
		"s3.secret-access-key": cfg.R2SecretAccessKey,
		"s3.region":            cfg.R2Region,
		"s3.path-style-access": "true",
	}
	return sqlcat.NewCatalog(cfg.Name, db, sqlcat.Postgres, props)
}

// Ensure catalog.Catalog is still satisfied — compile-time guard.
var _ catalog.Catalog = (catalog.Catalog)(nil)
