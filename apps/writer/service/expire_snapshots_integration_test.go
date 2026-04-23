//go:build integration

package service_test

import (
	"testing"
)

// TestExpireSnapshots_Integration is a stub for the full end-to-end expiry test.
//
// TODO(wave-c-tests): spin up testcontainers/postgres + minio,
// initialise the SQL catalog, create tables in AppendOnlyTables, write
// enough snapshots to trigger expiry, call ExpireSnapshots, and assert:
//   - returned ExpiredTotal > 0
//   - each table's snapshot count decreased as expected
//   - snapshots newer than OlderThan and/or within MinSnapshotsToKeep are retained
func TestExpireSnapshots_Integration(t *testing.T) {
	t.Skip("integration test stub — see TODO comment above")
}
