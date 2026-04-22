//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeR2Snapshotter captures UploadPublicSnapshot calls for assertion.
type fakeR2Snapshotter struct {
	uploads map[string][]byte
}

func (f *fakeR2Snapshotter) UploadPublicSnapshot(_ context.Context, key string, body []byte) error {
	if f.uploads == nil {
		f.uploads = map[string][]byte{}
	}
	f.uploads[key] = body
	return nil
}
func (f *fakeR2Snapshotter) TriggerDeploy() error { return nil }

// TestBackfillIceberg_Integration is a placeholder for Wave 7 testcontainer
// wiring once a local Iceberg catalog (REST or SQL) can be spun up in CI.
//
// TODO(wave7): wire a testcontainer-backed Iceberg catalog + MinIO,
// seed jobs.canonicals_current with fixture rows, and assert:
//   - rows above min_quality are uploaded to the snapshotter
//   - rows below min_quality are skipped
//   - the done JSON includes correct total/uploaded/skipped counts
func TestBackfillIceberg_Integration(t *testing.T) {
	t.Skip("TODO(wave7): requires Iceberg catalog testcontainer")

	ctx := context.Background()
	_ = ctx

	snap := &fakeR2Snapshotter{}

	// cat would be constructed from a testcontainer catalog in Wave 7.
	// h := backfillIcebergHandler(cat, snap, 50.0)

	req := httptest.NewRequest("POST", "/admin/backfill?min_quality=50", nil)
	rr := httptest.NewRecorder()
	_ = req
	_ = rr

	require.Empty(t, snap.uploads)

	var result map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&result))
	require.Equal(t, true, result["done"])
}
