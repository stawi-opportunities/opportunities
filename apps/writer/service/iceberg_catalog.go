package service

// iceberg_catalog.go — thin wrapper around catalog.Load, plus a
// retry helper that wraps Transaction.Append + Commit with
// exponential backoff. Frame redelivers on error, so returning an
// error on transient catalog failures is safe.

import (
	"context"
	"fmt"
	"time"

	"github.com/apache/arrow-go/v18/arrow/array"
	iceberg "github.com/apache/iceberg-go"
	"github.com/apache/iceberg-go/catalog"
	_ "github.com/apache/iceberg-go/catalog/sql" // register sql catalog type
	"github.com/apache/iceberg-go/table"
	"github.com/pitabwire/util"
)

// CatalogConfig carries all parameters needed to open the Iceberg SQL catalog.
type CatalogConfig struct {
	CatalogURI        string // postgres://...
	WarehouseURI      string // s3://stawi-jobs-log/iceberg
	R2Endpoint        string
	R2AccessKeyID     string
	R2SecretAccessKey string
	R2Region          string
	Name              string // "stawi"
}

// LoadIcebergCatalog opens a named SQL catalog backed by Postgres + R2.
func LoadIcebergCatalog(ctx context.Context, cfg CatalogConfig) (catalog.Catalog, error) {
	props := iceberg.Properties{
		"type":                  "sql",
		"uri":                   cfg.CatalogURI,
		"sql.dialect":           "postgres",
		"warehouse":             cfg.WarehouseURI,
		"s3.endpoint":           cfg.R2Endpoint,
		"s3.access-key-id":      cfg.R2AccessKeyID,
		"s3.secret-access-key":  cfg.R2SecretAccessKey,
		"s3.region":             cfg.R2Region,
		"s3.path-style-access":  "true",
	}
	return catalog.Load(ctx, cfg.Name, props)
}

// CommitBatchWithRetry appends a batch to an Iceberg table via
// Transaction.Append + Commit, retrying transient failures with
// exponential backoff. buildRecord is called once per attempt so each
// attempt gets a fresh RecordReader (callers must not re-use the
// reader after it has been released).
//
// The returned *table.Table is the refreshed table state after a
// successful commit. On failure after maxAttempts the last error is
// returned; Frame will redeliver the batch.
func CommitBatchWithRetry(
	ctx context.Context,
	tbl *table.Table,
	buildRecord func() (array.RecordReader, error),
	maxAttempts int,
) (*table.Table, error) {
	var lastErr error
	wait := 100 * time.Millisecond

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		rdr, err := buildRecord()
		if err != nil {
			// Build errors (bad JSON, schema mismatch) are not transient —
			// return immediately so Frame dead-letters the message.
			return nil, fmt.Errorf("build record batch: %w", err)
		}

		txn := tbl.NewTransaction()
		if err := txn.Append(ctx, rdr, nil); err != nil {
			rdr.Release()
			lastErr = err
			goto retry
		}
		rdr.Release()

		if t, err := txn.Commit(ctx); err != nil {
			lastErr = err
			goto retry
		} else {
			return t, nil
		}

	retry:
		util.Log(ctx).WithError(lastErr).
			WithField("attempt", attempt).
			Warn("iceberg commit failed; retrying")
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		wait *= 2
		if wait > 10*time.Second {
			wait = 10 * time.Second
		}
	}

	return nil, fmt.Errorf("iceberg commit exceeded %d retries: %w", maxAttempts, lastErr)
}
