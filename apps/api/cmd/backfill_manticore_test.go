//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBackfillManticore_Integration is a placeholder for Wave 7 testcontainer
// wiring once a local Manticore instance can be spun up in CI.
//
// TODO(wave7): wire a testcontainer-backed Manticore instance, seed
// idx_jobs_rt with fixture rows (active + inactive, varying quality_score),
// and assert:
//   - rows above min_quality are uploaded to the snapshotter
//   - rows below min_quality are skipped
//   - inactive rows are never uploaded
//   - the done JSON includes correct total/uploaded/skipped counts
//   - pagination works correctly when total > pageSize
func TestBackfillManticore_Integration(t *testing.T) {
	t.Skip("TODO(wave7): requires Manticore testcontainer")

	ctx := context.Background()
	_ = ctx

	snap := &fakeR2Snapshotter{}

	// jm would be constructed from a testcontainer Manticore in Wave 7.
	// h := backfillManticoreHandler(jm, snap, 50.0)

	req := httptest.NewRequest("POST", "/admin/backfill?min_quality=50", nil)
	rr := httptest.NewRecorder()
	_ = req
	_ = rr

	require.Empty(t, snap.uploads)

	var result map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&result))
	require.Equal(t, true, result["done"])
}

// TestScrollActive_Unit tests the ScrollActive pagination logic against
// a fake search backend.
func TestScrollActive_Unit(t *testing.T) {
	// Not an integration test — uses in-process fake. No build tag needed but
	// kept here alongside the integration stub for discoverability.
	t.Skip("TODO(wave7): implement fake Manticore HTTP server for unit pagination test")
}
