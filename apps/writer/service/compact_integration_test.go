//go:build integration

package service_test

import (
	"testing"
)

// TestCompact_Integration is a stub for the full end-to-end compaction test.
//
// TODO(wave-c-tests): spin up testcontainers/postgres + minio,
// initialise the SQL catalog, create a table in AppendOnlyTables, write
// enough small Parquet files to trigger compaction, call Compact, and assert:
//   - returned PartitionsProcessed > 0
//   - returned FilesBefore > FilesAfter
//   - returned BytesRewritten > 0
//   - the resulting table has fewer data files than before the compact run
//   - repeated calls to Compact are idempotent (nothing to compact after first run)
func TestCompact_Integration(t *testing.T) {
	t.Skip("integration test stub — see TODO comment above")
}
