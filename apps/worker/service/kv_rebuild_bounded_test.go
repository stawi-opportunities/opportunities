package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"stawi.jobs/pkg/kv"
)

// newMiniredisClient starts an in-memory Redis server and returns a go-redis
// client connected to it.
func newMiniredisClient(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	return c, mr
}

// TestFlushToValkey_Basic verifies that flushToValkey writes all entries and
// returns the correct count.
func TestFlushToValkey_Basic(t *testing.T) {
	c, _ := newMiniredisClient(t)
	r := &KVRebuilder{kv: c, bucket: "test-bucket"}

	ts := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	m := map[string]canonicalMinimal{
		"c1": {ClusterID: "c1", Title: "Job A", OccurredAt: ts},
		"c2": {ClusterID: "c2", Title: "Job B", OccurredAt: ts.Add(time.Hour)},
	}

	n, err := r.flushToValkey(context.Background(), m)
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	// Verify the keys exist and are readable as plain JSON strings.
	for id := range m {
		val, err := c.Get(context.Background(), "cluster:"+id).Result()
		require.NoError(t, err, "cluster:%s should exist", id)
		var snap kv.ClusterSnapshot
		require.NoError(t, json.Unmarshal([]byte(val), &snap))
		assert.Equal(t, id, snap.ClusterID)
	}
}

// TestFlushToValkey_CAS_NewerWins verifies the Lua CAS script: if a key
// already exists with an older ts, the newer entry replaces it.
func TestFlushToValkey_CAS_NewerWins(t *testing.T) {
	c, _ := newMiniredisClient(t)
	r := &KVRebuilder{kv: c, bucket: "test-bucket"}
	ctx := context.Background()

	old := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := old.Add(24 * time.Hour)

	// First flush: older entry written as plain JSON (matching Frame's format).
	oldJSON, _ := json.Marshal(kv.ClusterSnapshot{ClusterID: "c1", Title: "Old Title", LastSeenAt: old})
	require.NoError(t, c.Set(ctx, "cluster:c1", string(oldJSON), 0).Err())

	// Second flush: newer entry should overwrite.
	m := map[string]canonicalMinimal{
		"c1": {ClusterID: "c1", Title: "New Title", LastSeenAt: newer, OccurredAt: newer},
	}
	n, err := r.flushToValkey(ctx, m)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	val, err := c.Get(ctx, "cluster:c1").Result()
	require.NoError(t, err)
	var snap kv.ClusterSnapshot
	require.NoError(t, json.Unmarshal([]byte(val), &snap))
	assert.Equal(t, "New Title", snap.Title)
}

// TestFlushToValkey_CAS_OlderLoses verifies the Lua CAS script: if a key
// already exists with a newer ts, an older entry is rejected.
func TestFlushToValkey_CAS_OlderLoses(t *testing.T) {
	c, _ := newMiniredisClient(t)
	r := &KVRebuilder{kv: c, bucket: "test-bucket"}
	ctx := context.Background()

	newer := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	older := newer.Add(-24 * time.Hour)

	// Pre-set a newer entry as plain JSON (matching Frame's format).
	newerJSON, _ := json.Marshal(kv.ClusterSnapshot{ClusterID: "c1", Title: "Newer Title", LastSeenAt: newer})
	require.NoError(t, c.Set(ctx, "cluster:c1", string(newerJSON), 0).Err())

	// Attempt to flush an older entry.
	m := map[string]canonicalMinimal{
		"c1": {ClusterID: "c1", Title: "Older Title", LastSeenAt: older, OccurredAt: older},
	}
	_, err := r.flushToValkey(ctx, m)
	require.NoError(t, err)

	// The existing (newer) entry must not be replaced.
	val, err := c.Get(ctx, "cluster:c1").Result()
	require.NoError(t, err)
	var snap kv.ClusterSnapshot
	require.NoError(t, json.Unmarshal([]byte(val), &snap))
	assert.Equal(t, "Newer Title", snap.Title, "older flush must not overwrite newer Valkey entry")
}

// TestBoundedMapFlushCycle verifies that when the bounded map is full it is
// flushed and reset, keeping total in-memory keys bounded.
//
// This is a unit test of the algorithm, not the full Run() path.
func TestBoundedMapFlushCycle(t *testing.T) {
	c, _ := newMiniredisClient(t)
	r := &KVRebuilder{kv: c, bucket: "test-bucket"}
	ctx := context.Background()

	maxKeys := 3
	bounded := make(map[string]canonicalMinimal, maxKeys)
	totalFlushed := 0

	now := time.Now()

	// Simulate streaming 10 distinct cluster IDs.
	rows := make([]canonicalMinimal, 10)
	for i := range rows {
		rows[i] = canonicalMinimal{
			ClusterID:  fmt.Sprintf("c%d", i),
			Title:      fmt.Sprintf("Job %d", i),
			OccurredAt: now.Add(time.Duration(i) * time.Second),
		}
	}

	for _, row := range rows {
		existing, ok := bounded[row.ClusterID]
		if !ok || row.OccurredAt.After(existing.OccurredAt) {
			bounded[row.ClusterID] = row
		}
		if len(bounded) >= maxKeys {
			n, err := r.flushToValkey(ctx, bounded)
			require.NoError(t, err)
			totalFlushed += n
			bounded = make(map[string]canonicalMinimal, maxKeys)
		}
	}
	// Final flush.
	n, err := r.flushToValkey(ctx, bounded)
	require.NoError(t, err)
	totalFlushed += n

	// All 10 unique cluster IDs should have been flushed.
	assert.Equal(t, 10, totalFlushed)

	// Verify each key exists in Valkey.
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("cluster:c%d", i)
		exists, err := c.Exists(ctx, key).Result()
		require.NoError(t, err)
		assert.Equal(t, int64(1), exists, "key %s should exist", key)
	}
}

// TestFlushToValkey_RoundTrip verifies that flushToValkey writes in the plain
// GET/SET format that Frame's cache.Cache[string, kv.ClusterSnapshot] reader
// expects: a plain string key whose value is JSON-encoded kv.ClusterSnapshot.
//
// This proves writer/reader compatibility end-to-end without requiring a full
// Frame wiring: write via flushToValkey, read via plain GET + json.Unmarshal.
func TestFlushToValkey_RoundTrip(t *testing.T) {
	c, _ := newMiniredisClient(t)
	r := &KVRebuilder{kv: c, bucket: "test-bucket"}
	ctx := context.Background()

	ts := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	m := map[string]canonicalMinimal{
		"rt1": {
			ClusterID:   "rt1",
			CanonicalID: "canon-42",
			Title:       "Senior Engineer",
			Company:     "Acme",
			Country:     "KE",
			LastSeenAt:  ts,
			OccurredAt:  ts,
		},
	}

	n, err := r.flushToValkey(ctx, m)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	// Read exactly as Frame's cache.GetCache path would: plain GET, then JSON decode.
	raw, err := c.Get(ctx, "cluster:rt1").Result()
	require.NoError(t, err, "key must be stored as a plain string")

	var snap kv.ClusterSnapshot
	require.NoError(t, json.Unmarshal([]byte(raw), &snap), "value must be valid JSON kv.ClusterSnapshot")

	assert.Equal(t, "rt1", snap.ClusterID)
	assert.Equal(t, "canon-42", snap.CanonicalID)
	assert.Equal(t, "Senior Engineer", snap.Title)
	assert.Equal(t, "Acme", snap.Company)
	assert.Equal(t, "KE", snap.Country)
	assert.True(t, snap.LastSeenAt.Equal(ts), "LastSeenAt must survive JSON round-trip")
}
