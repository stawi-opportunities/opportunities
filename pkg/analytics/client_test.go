package analytics

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestClient_Send_batchesAndFlushes(t *testing.T) {
	var (
		mu       sync.Mutex
		received []map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var batch []map[string]any
		_ = json.Unmarshal(body, &batch)
		mu.Lock()
		received = append(received, batch...)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(Config{
		BaseURL:       srv.URL,
		Org:           "default",
		MaxBatchSize:  3,
		FlushInterval: 50 * time.Millisecond,
	})
	defer func() { _ = c.Close(context.Background()) }()

	for i := 0; i < 5; i++ {
		c.Send(context.Background(), "test_stream", map[string]any{"i": i})
	}

	// Wait for both the size-triggered flush (first 3) and the timer
	// flush for the remaining 2.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := len(received)
		mu.Unlock()
		if got >= 5 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 5 {
		t.Errorf("expected 5 events, got %d", len(received))
	}
	for _, e := range received {
		if _, ok := e["_timestamp"]; !ok {
			t.Errorf("event missing auto-stamped _timestamp: %+v", e)
		}
	}
}

func TestClient_Send_nilIsNoop(t *testing.T) {
	// Should not panic.
	var c *Client
	c.Send(context.Background(), "x", map[string]any{"a": 1})
	_ = c.Close(context.Background())
}

func TestNew_emptyURLReturnsNil(t *testing.T) {
	if c := New(Config{BaseURL: ""}); c != nil {
		t.Error("expected nil client when BaseURL empty")
	}
}

func TestClient_Send_streamsIsolated(t *testing.T) {
	var (
		mu       sync.Mutex
		perPath  = map[string]int{}
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var batch []map[string]any
		_ = json.Unmarshal(body, &batch)
		mu.Lock()
		perPath[r.URL.Path] += len(batch)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, MaxBatchSize: 100, FlushInterval: 30 * time.Millisecond})
	defer func() { _ = c.Close(context.Background()) }()

	c.Send(context.Background(), "stream_a", map[string]any{"x": 1})
	c.Send(context.Background(), "stream_b", map[string]any{"x": 2})
	c.Send(context.Background(), "stream_a", map[string]any{"x": 3})

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		a, b := perPath["/api/default/stream_a/_json"], perPath["/api/default/stream_b/_json"]
		mu.Unlock()
		if a == 2 && b == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Errorf("per-stream routing failed: %+v", perPath)
}
