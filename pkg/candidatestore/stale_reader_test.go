//go:build integration

// Integration tests for candidatestore.StaleReader against a live
// Iceberg catalog backed by MinIO + Postgres. Stubs kept here until
// Wave 7's testcontainer helpers are available.
//
// TODO(wave7): replace stubs with real Iceberg-against-MinIO tests once
// the testcontainer helpers from apps/iceberg-ops/testutil are available.
package candidatestore

import (
	"context"
	"testing"
	"time"
)

func TestStaleReader_ListStale(t *testing.T) {
	t.Skip("Wave 7 Iceberg testcontainer scaffolding not yet available")

	ctx := context.Background()
	cutoff := time.Now().UTC().Add(-60 * 24 * time.Hour)
	_ = ctx
	_ = cutoff
}
