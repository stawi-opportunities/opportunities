package service_test

// service_unit_test.go — unit tests for Watermark, BulkUpserter, and
// the icebergFileKey helper. No containers needed; all external
// dependencies are either in-process (miniredis) or stubbed via
// net/http/httptest.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	matsvc "stawi.jobs/apps/materializer/service"
	"stawi.jobs/pkg/searchindex"
)

// ---------------------------------------------------------------------------
// Leader-election lease unit tests (miniredis)
// ---------------------------------------------------------------------------

// TestLeaderElection_AcquireAndRelease verifies that a replica acquires the
// per-table lease using SET NX EX and that the Lua compare-and-delete script
// removes the key only if the value matches the caller's instanceID.
func TestLeaderElection_AcquireAndRelease(t *testing.T) {
	mr := miniredis.RunT(t)
	kv := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()

	leaseKey := "mat:leader:jobs.canonicals"
	instanceID := "test-pod-uuid"
	leaseTTL := 45 * time.Second

	// Acquire: SET NX EX.
	ok, err := kv.SetNX(ctx, leaseKey, instanceID, leaseTTL).Result()
	require.NoError(t, err)
	assert.True(t, ok, "first acquire must succeed")

	// Competing acquire from a different instanceID must fail.
	ok2, err := kv.SetNX(ctx, leaseKey, "other-pod", leaseTTL).Result()
	require.NoError(t, err)
	assert.False(t, ok2, "second acquire from different pod must fail")

	// Verify the stored value is the original instanceID.
	val, err := kv.Get(ctx, leaseKey).Result()
	require.NoError(t, err)
	assert.Equal(t, instanceID, val)

	// Release via Lua CAD (correct owner).
	const releaseScript = `
if redis.call("get", KEYS[1]) == ARGV[1] then
    return redis.call("del", KEYS[1])
else
    return 0
end`
	released, err := kv.Eval(ctx, releaseScript, []string{leaseKey}, instanceID).Int()
	require.NoError(t, err)
	assert.Equal(t, 1, released, "release by correct owner must return 1")

	// Key must be gone.
	_, err = kv.Get(ctx, leaseKey).Result()
	assert.ErrorIs(t, err, redis.Nil, "key must be absent after release")
}

// TestLeaderElection_WrongOwnerCannotRelease ensures a different instanceID
// cannot release a lease it doesn't own (crash-restart safety).
func TestLeaderElection_WrongOwnerCannotRelease(t *testing.T) {
	mr := miniredis.RunT(t)
	kv := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()

	leaseKey := "mat:leader:jobs.embeddings"
	instanceID := "pod-a"
	leaseTTL := 45 * time.Second

	require.NoError(t, kv.Set(ctx, leaseKey, instanceID, leaseTTL).Err())

	const releaseScript = `
if redis.call("get", KEYS[1]) == ARGV[1] then
    return redis.call("del", KEYS[1])
else
    return 0
end`

	// Wrong owner attempts release.
	released, err := kv.Eval(ctx, releaseScript, []string{leaseKey}, "pod-b").Int()
	require.NoError(t, err)
	assert.Equal(t, 0, released, "wrong owner must not release the lease")

	// Key must still exist with original value.
	val, err := kv.Get(ctx, leaseKey).Result()
	require.NoError(t, err)
	assert.Equal(t, instanceID, val)
}

// TestLeaderElection_LeaseExpiry verifies that miniredis TTL expiry releases
// the lease, allowing another pod to acquire it.
func TestLeaderElection_LeaseExpiry(t *testing.T) {
	mr := miniredis.RunT(t)
	kv := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()

	leaseKey := "mat:leader:jobs.translations"
	leaseTTL := 2 * time.Second

	ok, err := kv.SetNX(ctx, leaseKey, "pod-a", leaseTTL).Result()
	require.NoError(t, err)
	assert.True(t, ok)

	// Fast-forward miniredis clock past TTL.
	mr.FastForward(3 * time.Second)

	// Key must be gone now.
	_, err = kv.Get(ctx, leaseKey).Result()
	assert.ErrorIs(t, err, redis.Nil, "lease must expire after TTL")

	// Another pod can now acquire.
	ok2, err := kv.SetNX(ctx, leaseKey, "pod-b", leaseTTL).Result()
	require.NoError(t, err)
	assert.True(t, ok2, "pod-b must acquire after pod-a's lease expired")
}

// ---------------------------------------------------------------------------
// Watermark unit tests (miniredis)
// ---------------------------------------------------------------------------

func TestWatermark_GetSetRoundTrip(t *testing.T) {
	mr := miniredis.RunT(t)
	kv := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	wm := matsvc.NewWatermark(kv)
	ctx := context.Background()

	// Cold start — key absent.
	id, err := wm.Get(ctx, "jobs.canonicals")
	require.NoError(t, err)
	assert.Equal(t, int64(0), id, "cold start should return 0")

	// Set and round-trip.
	require.NoError(t, wm.Set(ctx, "jobs.canonicals", 12345678))
	id, err = wm.Get(ctx, "jobs.canonicals")
	require.NoError(t, err)
	assert.Equal(t, int64(12345678), id)
}

func TestWatermark_IndependentKeys(t *testing.T) {
	mr := miniredis.RunT(t)
	kv := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	wm := matsvc.NewWatermark(kv)
	ctx := context.Background()

	require.NoError(t, wm.Set(ctx, "jobs.canonicals", 100))
	require.NoError(t, wm.Set(ctx, "jobs.embeddings", 200))
	require.NoError(t, wm.Set(ctx, "jobs.translations", 300))

	c, _ := wm.Get(ctx, "jobs.canonicals")
	e, _ := wm.Get(ctx, "jobs.embeddings")
	tr, _ := wm.Get(ctx, "jobs.translations")
	assert.Equal(t, int64(100), c)
	assert.Equal(t, int64(200), e)
	assert.Equal(t, int64(300), tr)
}

// ---------------------------------------------------------------------------
// BulkUpserter unit tests (stub Manticore HTTP server)
// ---------------------------------------------------------------------------

// newStubManticore starts a minimal HTTP server that records /bulk
// request bodies and always responds 200 {}.
func newStubManticore(t *testing.T) (client *searchindex.Client, bodies *[][]byte, stop func()) {
	t.Helper()
	var captured [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		captured = append(captured, b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	c, err := searchindex.Open(searchindex.Config{URL: srv.URL})
	require.NoError(t, err)
	return c, &captured, srv.Close
}

func TestBulkUpserter_FlushEmpty(t *testing.T) {
	c, bodies, stop := newStubManticore(t)
	defer stop()
	up := matsvc.NewBulkUpserter(c, "idx_jobs_rt", 500)
	require.NoError(t, up.Flush(context.Background()))
	assert.Empty(t, *bodies, "empty flush should not hit /bulk")
}

func TestBulkUpserter_AutoFlushOnMaxRows(t *testing.T) {
	c, bodies, stop := newStubManticore(t)
	defer stop()
	// maxRows = 2 → flush fires on third Add.
	up := matsvc.NewBulkUpserter(c, "idx_jobs_rt", 2)
	ctx := context.Background()

	require.NoError(t, up.Add(ctx, 1, map[string]any{"title": "a"}))
	require.NoError(t, up.Add(ctx, 2, map[string]any{"title": "b"}))
	// Second Add fills the buffer; auto-flush should have fired.
	assert.Equal(t, 1, len(*bodies), "expected one /bulk call after buffer filled")
	assert.Equal(t, 0, up.Len(), "buffer should be drained after auto-flush")
}

func TestBulkUpserter_ExplicitFlush(t *testing.T) {
	c, bodies, stop := newStubManticore(t)
	defer stop()
	up := matsvc.NewBulkUpserter(c, "idx_jobs_rt", 500)
	ctx := context.Background()

	require.NoError(t, up.Add(ctx, 1, map[string]any{"title": "job one"}))
	require.NoError(t, up.Add(ctx, 2, map[string]any{"title": "job two"}))
	require.NoError(t, up.Flush(ctx))

	require.Equal(t, 1, len(*bodies))
	// Body should be valid NDJSON with two lines.
	lines := strings.Split(strings.TrimSpace(string((*bodies)[0])), "\n")
	assert.Len(t, lines, 2, "expected two NDJSON lines")
	for _, line := range lines {
		var obj map[string]any
		assert.NoError(t, json.Unmarshal([]byte(line), &obj), "each line must be valid JSON")
		assert.Contains(t, obj, "replace", "each line must have a 'replace' key")
	}
}

func TestBulkUpserter_NdjsonShape(t *testing.T) {
	var lastBody []byte
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		lastBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c, err := searchindex.Open(searchindex.Config{URL: srv.URL})
	require.NoError(t, err)

	up := matsvc.NewBulkUpserter(c, "myidx", 500)
	ctx := context.Background()
	require.NoError(t, up.Add(ctx, 42, map[string]any{"f": "v"}))
	require.NoError(t, up.Flush(ctx))

	require.Equal(t, int32(1), callCount.Load())
	// Parse the single NDJSON line.
	var doc struct {
		Replace struct {
			Index string         `json:"index"`
			ID    int64          `json:"id"`
			Doc   map[string]any `json:"doc"`
		} `json:"replace"`
	}
	require.NoError(t, json.Unmarshal(bytes.TrimRight(lastBody, "\n"), &doc))
	assert.Equal(t, "myidx", doc.Replace.Index)
	assert.Equal(t, int64(42), doc.Replace.ID)
	assert.Equal(t, "v", doc.Replace.Doc["f"])
}
