package service_test

// service_unit_test.go — unit tests for BulkUpserter (the Manticore
// bulk-upsert helper that handlers may call in future batching variants).
//
// Leader-election and Watermark tests removed: those subsystems are gone
// from the materializer. Frame's NATS JetStream consumer group handles
// deduplication and redelivery; consumer lag is observable via the NATS
// Prometheus exporter's `nats_jetstream_consumer_num_pending` metric.

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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	matsvc "github.com/stawi-opportunities/opportunities/apps/materializer/service"
	"github.com/stawi-opportunities/opportunities/pkg/searchindex"
)

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
	up := matsvc.NewBulkUpserter(c, "idx_opportunities_rt", 500)
	require.NoError(t, up.Flush(context.Background()))
	assert.Empty(t, *bodies, "empty flush should not hit /bulk")
}

func TestBulkUpserter_AutoFlushOnMaxRows(t *testing.T) {
	c, bodies, stop := newStubManticore(t)
	defer stop()
	// maxRows = 2 → flush fires on second Add.
	up := matsvc.NewBulkUpserter(c, "idx_opportunities_rt", 2)
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
	up := matsvc.NewBulkUpserter(c, "idx_opportunities_rt", 500)
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
