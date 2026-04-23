package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/iceberg-go/catalog"
	"github.com/apache/iceberg-go/table"
	"github.com/pitabwire/util"

	"stawi.jobs/pkg/telemetry"
)

// CommitBatchWithRetry applies a batch to a table with exponential backoff.
// Reloads the table metadata on each attempt so OCC conflicts resolve
// cleanly: attempt N+1 sees the commit from attempt N on the other replica
// and its own Append is applied against the fresh metadata.
//
// Records the following OTel metrics via pkg/telemetry:
//   - IcebergCommitsTotal    (attrs: table, outcome=ok|retry|fail)
//   - IcebergCommitDuration  (attrs: table, outcome)
//   - IcebergCommitRetries   (attrs: table) — incremented per retry, not per call
//   - IcebergCommitRowsTotal (attrs: table) — incremented on success
func CommitBatchWithRetry(
	ctx context.Context,
	cat catalog.Catalog,
	ident []string,
	buildRecord func() (array.RecordReader, error),
	maxAttempts int,
) (*table.Table, error) {
	tableName := strings.Join(ident, ".")
	start := time.Now()

	var lastErr error
	wait := 100 * time.Millisecond
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		t, err := commitOnce(ctx, cat, ident, buildRecord)
		if err == nil {
			dur := time.Since(start).Seconds()
			var rows int64
			if snap := t.Metadata().CurrentSnapshot(); snap != nil {
				if summary := snap.Summary; summary != nil {
					if v, ok := summary.Properties["added-records"]; ok {
						_, _ = fmt.Sscanf(v, "%d", &rows)
					}
				}
			}
			telemetry.RecordCommit(tableName, "ok", dur, rows)
			return t, nil
		}
		// Record-build failures are not transient (schema mismatch,
		// decode error). Fail fast; don't burn retries.
		if buildErr, ok := err.(buildRecordError); ok {
			telemetry.RecordCommit(tableName, "fail", time.Since(start).Seconds(), 0)
			return nil, buildErr.err
		}
		lastErr = err
		if attempt < maxAttempts {
			telemetry.RecordCommitRetry(tableName)
		}
		util.Log(ctx).WithError(lastErr).
			WithField("attempt", attempt).
			WithField("ident", ident).
			Warn("iceberg commit failed; retrying with fresh table load")
		select {
		case <-ctx.Done():
			telemetry.RecordCommit(tableName, "fail", time.Since(start).Seconds(), 0)
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		wait *= 2
		if wait > 10*time.Second {
			wait = 10 * time.Second
		}
	}
	telemetry.RecordCommit(tableName, "fail", time.Since(start).Seconds(), 0)
	return nil, fmt.Errorf("iceberg commit exceeded retries: %w", lastErr)
}

// buildRecordError wraps a record-build failure so CommitBatchWithRetry
// can distinguish it from a transient catalog/network error and fail fast.
type buildRecordError struct{ err error }

func (e buildRecordError) Error() string { return e.err.Error() }

// commitOnce loads the table and attempts a single Append+Commit transaction.
func commitOnce(
	ctx context.Context,
	cat catalog.Catalog,
	ident []string,
	buildRecord func() (array.RecordReader, error),
) (*table.Table, error) {
	tbl, err := cat.LoadTable(ctx, ident)
	if err != nil {
		return nil, fmt.Errorf("load table: %w", err)
	}
	rdr, err := buildRecord()
	if err != nil {
		return nil, buildRecordError{err: fmt.Errorf("build record batch: %w", err)}
	}
	txn := tbl.NewTransaction()
	if err := txn.Append(ctx, rdr, nil); err != nil {
		rdr.Release()
		return nil, err
	}
	rdr.Release()
	t, err := txn.Commit(ctx)
	if err != nil {
		return nil, err
	}
	return t, nil
}
