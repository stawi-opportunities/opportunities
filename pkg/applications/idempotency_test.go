//go:build integration

package applications_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/applications"
)

func TestIdempotencyStore_LookupSaveRound(t *testing.T) {
	db, ctx := setupStoreDB(t)
	s := applications.NewIdempotencyStore(db, time.Hour)

	miss, err := s.Lookup(ctx, "c1", "key-1", "applications.create")
	require.NoError(t, err)
	require.Nil(t, miss)

	resp := applications.StoredResponse{
		StatusCode: 201,
		Body:       json.RawMessage(`{"application_id":"a1"}`),
		Headers:    map[string]string{"X-Test": "1"},
	}
	require.NoError(t, s.Save(ctx, "c1", "key-1", "applications.create", resp))

	hit, err := s.Lookup(ctx, "c1", "key-1", "applications.create")
	require.NoError(t, err)
	require.NotNil(t, hit)
	require.Equal(t, 201, hit.StatusCode)
	require.JSONEq(t, `{"application_id":"a1"}`, string(hit.Body))
	require.Equal(t, "1", hit.Headers["X-Test"])
}

func TestIdempotencyStore_SaveSecondTimeKeepsFirstResponse(t *testing.T) {
	db, ctx := setupStoreDB(t)
	s := applications.NewIdempotencyStore(db, time.Hour)
	first := applications.StoredResponse{StatusCode: 201, Body: json.RawMessage(`"first"`)}
	require.NoError(t, s.Save(ctx, "c", "k", "rg", first))
	second := applications.StoredResponse{StatusCode: 500, Body: json.RawMessage(`"second"`)}
	require.NoError(t, s.Save(ctx, "c", "k", "rg", second))

	hit, err := s.Lookup(ctx, "c", "k", "rg")
	require.NoError(t, err)
	require.Equal(t, 201, hit.StatusCode)
	require.JSONEq(t, `"first"`, string(hit.Body))
}

func TestIdempotencyStore_Purge(t *testing.T) {
	db, ctx := setupStoreDB(t)
	s := applications.NewIdempotencyStore(db, time.Hour)
	require.NoError(t, s.Save(ctx, "c", "k", "rg", applications.StoredResponse{StatusCode: 200, Body: json.RawMessage("null")}))

	// Force the row to be expired.
	_, err := db.ExecContext(ctx, `UPDATE idempotency_keys SET expires_at = now() - INTERVAL '1 minute'`)
	require.NoError(t, err)
	n, err := s.Purge(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, int64(1))

	miss, err := s.Lookup(ctx, "c", "k", "rg")
	require.NoError(t, err)
	require.Nil(t, miss)
}
