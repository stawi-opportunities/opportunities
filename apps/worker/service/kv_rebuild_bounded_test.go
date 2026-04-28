package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/pitabwire/frame/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/kv"
)

// newInMemoryRawCache returns an in-memory cache.RawCache for tests.
// Frame's cache.NewInMemoryCache satisfies the same RawCache contract
// as the production valkey driver.
func newInMemoryRawCache(t *testing.T) cache.RawCache {
	t.Helper()
	rc := cache.NewInMemoryCache()
	t.Cleanup(func() { _ = rc.Close() })
	return rc
}

// TestFlushToValkey_Basic verifies that flushToValkey writes all entries and
// returns the correct count.
func TestFlushToValkey_Basic(t *testing.T) {
	c := newInMemoryRawCache(t)
	r := &KVRebuilder{cache: c, bucket: "test-bucket"}

	ts := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	m := map[string]canonicalMinimal{
		"c1": {ClusterID: "c1", Title: "Job A", LastSeenAt: ts, OccurredAt: ts},
		"c2": {ClusterID: "c2", Title: "Job B", LastSeenAt: ts.Add(time.Hour), OccurredAt: ts.Add(time.Hour)},
	}

	n, err := r.flushToValkey(context.Background(), m)
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	// Verify the keys exist and are readable as plain JSON strings.
	for id := range m {
		raw, found, err := c.Get(context.Background(), "cluster:"+id)
		require.NoError(t, err)
		require.True(t, found, "cluster:%s should exist", id)
		var snap kv.ClusterSnapshot
		require.NoError(t, json.Unmarshal(raw, &snap))
		assert.Equal(t, id, snap.ClusterID)
	}
}

// TestFlushToValkey_CAS_NewerWins verifies the GET+conditional-SET path:
// if a key already exists with an older ts, the newer entry replaces it.
func TestFlushToValkey_CAS_NewerWins(t *testing.T) {
	c := newInMemoryRawCache(t)
	r := &KVRebuilder{cache: c, bucket: "test-bucket"}
	ctx := context.Background()

	old := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := old.Add(24 * time.Hour)

	// First flush: older entry written as plain JSON.
	oldJSON, _ := json.Marshal(kv.ClusterSnapshot{ClusterID: "c1", Title: "Old Title", LastSeenAt: old})
	require.NoError(t, c.Set(ctx, "cluster:c1", oldJSON, 0))

	// Second flush: newer entry should overwrite.
	m := map[string]canonicalMinimal{
		"c1": {ClusterID: "c1", Title: "New Title", LastSeenAt: newer, OccurredAt: newer},
	}
	n, err := r.flushToValkey(ctx, m)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	raw, found, err := c.Get(ctx, "cluster:c1")
	require.NoError(t, err)
	require.True(t, found)
	var snap kv.ClusterSnapshot
	require.NoError(t, json.Unmarshal(raw, &snap))
	assert.Equal(t, "New Title", snap.Title)
}

// TestFlushToValkey_CAS_OlderLoses verifies the GET+conditional-SET path:
// if a key already exists with a newer ts, an older entry is rejected.
func TestFlushToValkey_CAS_OlderLoses(t *testing.T) {
	c := newInMemoryRawCache(t)
	r := &KVRebuilder{cache: c, bucket: "test-bucket"}
	ctx := context.Background()

	newer := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	older := newer.Add(-24 * time.Hour)

	// Pre-set a newer entry as plain JSON (matching Frame's format).
	newerJSON, _ := json.Marshal(kv.ClusterSnapshot{ClusterID: "c1", Title: "Newer Title", LastSeenAt: newer})
	require.NoError(t, c.Set(ctx, "cluster:c1", newerJSON, 0))

	// Attempt to flush an older entry.
	m := map[string]canonicalMinimal{
		"c1": {ClusterID: "c1", Title: "Older Title", LastSeenAt: older, OccurredAt: older},
	}
	n, err := r.flushToValkey(ctx, m)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "older flush must report 0 keys written")

	// The existing (newer) entry must not be replaced.
	raw, found, err := c.Get(ctx, "cluster:c1")
	require.NoError(t, err)
	require.True(t, found)
	var snap kv.ClusterSnapshot
	require.NoError(t, json.Unmarshal(raw, &snap))
	assert.Equal(t, "Newer Title", snap.Title, "older flush must not overwrite newer entry")
}

// TestBoundedMapFlushCycle verifies that when the bounded map is full it is
// flushed and reset, keeping total in-memory keys bounded.
//
// This is a unit test of the algorithm, not the full Run() path.
func TestBoundedMapFlushCycle(t *testing.T) {
	c := newInMemoryRawCache(t)
	r := &KVRebuilder{cache: c, bucket: "test-bucket"}
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
			LastSeenAt: now.Add(time.Duration(i) * time.Second),
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

	// Verify each key exists in the cache.
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("cluster:c%d", i)
		exists, err := c.Exists(ctx, key)
		require.NoError(t, err)
		assert.True(t, exists, "key %s should exist", key)
	}
}

// TestFlushToValkey_RoundTrip verifies that flushToValkey writes in the plain
// GET/SET format that Frame's cache.Cache[string, kv.ClusterSnapshot] reader
// expects: a plain string key whose value is JSON-encoded kv.ClusterSnapshot.
//
// This proves writer/reader compatibility end-to-end without requiring a full
// Frame wiring: write via flushToValkey, read via plain Get + json.Unmarshal.
func TestFlushToValkey_RoundTrip(t *testing.T) {
	c := newInMemoryRawCache(t)
	r := &KVRebuilder{cache: c, bucket: "test-bucket"}
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

	// Read exactly as Frame's cache.GetCache path would: plain Get, then JSON decode.
	raw, found, err := c.Get(ctx, "cluster:rt1")
	require.NoError(t, err)
	require.True(t, found, "key must be stored")

	var snap kv.ClusterSnapshot
	require.NoError(t, json.Unmarshal(raw, &snap), "value must be valid JSON kv.ClusterSnapshot")

	assert.Equal(t, "rt1", snap.ClusterID)
	assert.Equal(t, "canon-42", snap.CanonicalID)
	assert.Equal(t, "Senior Engineer", snap.Title)
	assert.Equal(t, "Acme", snap.Company)
	assert.Equal(t, "KE", snap.Country)
	assert.True(t, snap.LastSeenAt.Equal(ts), "LastSeenAt must survive JSON round-trip")
}
