package service

// iceberg_catalog.go — thin wrapper around catalog.Load, plus a
// retry helper that wraps Transaction.Append + Commit with
// exponential backoff. Frame redelivers on error, so returning an
// error on transient catalog failures is safe.
//
// Deprecated: CatalogConfig and LoadIcebergCatalog are re-exported
// from pkg/icebergclient for use by other packages. New callers should
// import stawi.jobs/pkg/icebergclient directly.

import (
	"context"
	"fmt"
	"time"

	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/iceberg-go/catalog"
	"github.com/apache/iceberg-go/table"
	"github.com/pitabwire/util"

	"stawi.jobs/pkg/icebergclient"
)

// CatalogConfig carries all parameters needed to open the Iceberg SQL catalog.
//
// Deprecated: use icebergclient.CatalogConfig instead.
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
//
// Deprecated: use icebergclient.LoadCatalog instead.
func LoadIcebergCatalog(ctx context.Context, cfg CatalogConfig) (catalog.Catalog, error) {
	return icebergclient.LoadCatalog(ctx, icebergclient.CatalogConfig{
		Name:              cfg.Name,
		URI:               cfg.CatalogURI,
		Warehouse:         cfg.WarehouseURI,
		R2Endpoint:        cfg.R2Endpoint,
		R2AccessKeyID:     cfg.R2AccessKeyID,
		R2SecretAccessKey: cfg.R2SecretAccessKey,
		R2Region:          cfg.R2Region,
	})
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
