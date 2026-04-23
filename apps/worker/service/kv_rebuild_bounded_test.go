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
	r := &KVRebuilder{kv: c}

	ts := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	m := map[string]canonicalMinimal{
		"c1": {ClusterID: "c1", Title: "Job A", OccurredAt: ts},
		"c2": {ClusterID: "c2", Title: "Job B", OccurredAt: ts.Add(time.Hour)},
	}

	n, err := r.flushToValkey(context.Background(), m)
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	// Verify the keys exist and json field is readable.
	for id := range m {
		jsonVal, err := c.HGet(context.Background(), "cluster:"+id, "json").Result()
		require.NoError(t, err, "cluster:%s should have json field", id)
		var snap kv.ClusterSnapshot
		require.NoError(t, json.Unmarshal([]byte(jsonVal), &snap))
		assert.Equal(t, id, snap.ClusterID)
	}
}

// TestFlushToValkey_CAS_NewerWins verifies the Lua CAS script: if a key
// already exists with an older ts, the newer entry replaces it.
func TestFlushToValkey_CAS_NewerWins(t *testing.T) {
	c, _ := newMiniredisClient(t)
	r := &KVRebuilder{kv: c}
	ctx := context.Background()

	old := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := old.Add(24 * time.Hour)

	// First flush: older entry.
	require.NoError(t, r.kv.HSet(ctx, "cluster:c1",
		"ts", old.UnixMilli(),
		"json", `{"cluster_id":"c1","title":"Old Title"}`).Err())

	// Second flush: newer entry should overwrite.
	m := map[string]canonicalMinimal{
		"c1": {ClusterID: "c1", Title: "New Title", OccurredAt: newer},
	}
	n, err := r.flushToValkey(ctx, m)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	jsonVal, err := c.HGet(ctx, "cluster:c1", "json").Result()
	require.NoError(t, err)
	var snap kv.ClusterSnapshot
	require.NoError(t, json.Unmarshal([]byte(jsonVal), &snap))
	assert.Equal(t, "New Title", snap.Title)
}

// TestFlushToValkey_CAS_OlderLoses verifies the Lua CAS script: if a key
// already exists with a newer ts, an older entry is rejected.
func TestFlushToValkey_CAS_OlderLoses(t *testing.T) {
	c, _ := newMiniredisClient(t)
	r := &KVRebuilder{kv: c}
	ctx := context.Background()

	newer := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	older := newer.Add(-24 * time.Hour)

	// Pre-set a newer entry.
	require.NoError(t, r.kv.HSet(ctx, "cluster:c1",
		"ts", newer.UnixMilli(),
		"json", `{"cluster_id":"c1","title":"Newer Title"}`).Err())

	// Attempt to flush an older entry.
	m := map[string]canonicalMinimal{
		"c1": {ClusterID: "c1", Title: "Older Title", OccurredAt: older},
	}
	_, err := r.flushToValkey(ctx, m)
	require.NoError(t, err)

	// The existing (newer) entry must not be replaced.
	jsonVal, err := c.HGet(ctx, "cluster:c1", "json").Result()
	require.NoError(t, err)
	var snap kv.ClusterSnapshot
	require.NoError(t, json.Unmarshal([]byte(jsonVal), &snap))
	assert.Equal(t, "Newer Title", snap.Title, "older flush must not overwrite newer Valkey entry")
}

// TestBoundedMapFlushCycle verifies that when the bounded map is full it is
// flushed and reset, keeping total in-memory keys bounded.
//
// This is a unit test of the algorithm, not the full Run() path.
func TestBoundedMapFlushCycle(t *testing.T) {
	c, _ := newMiniredisClient(t)
	r := &KVRebuilder{kv: c}
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
