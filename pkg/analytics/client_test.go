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

// After Close, any buffered events must still reach the server.
func TestClient_Close_drainsPending(t *testing.T) {
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

	// MaxBatchSize=100 + long FlushInterval → nothing auto-flushes before Close.
	c := New(Config{BaseURL: srv.URL, MaxBatchSize: 100, FlushInterval: 1 * time.Hour})
	for i := 0; i < 3; i++ {
		c.Send(context.Background(), "drain_test", map[string]any{"i": i})
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("Close returned err: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 3 {
		t.Errorf("drain landed %d events, want 3", len(received))
	}
}

// flush is private but we observe its error path: a 500 from the server
// should surface via the returned Close error.
func TestClient_flush_propagatesHTTPFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, MaxBatchSize: 100, FlushInterval: 1 * time.Hour})
	c.Send(context.Background(), "fail_stream", map[string]any{"x": 1})

	err := c.Close(context.Background())
	if err == nil {
		t.Fatal("expected error from failing flush on Close, got nil")
	}
}

// Every received event should carry a numeric _timestamp stamped by the
// client when the caller didn't supply one.
func TestSend_autoStampsTimestamp(t *testing.T) {
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

	c := New(Config{BaseURL: srv.URL, MaxBatchSize: 100, FlushInterval: 1 * time.Hour})
	c.Send(context.Background(), "stamp_test", map[string]any{"k": "v"})
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("received %d events, want 1", len(received))
	}
	ts, ok := received[0]["_timestamp"]
	if !ok {
		t.Fatalf("event missing _timestamp: %+v", received[0])
	}
	// JSON decodes numeric into float64.
	f, isNum := ts.(float64)
	if !isNum {
		t.Fatalf("_timestamp %T not numeric (%v)", ts, ts)
	}
	if f <= 0 {
		t.Errorf("_timestamp = %v, want > 0", f)
	}
}

// A caller-provided _timestamp must be preserved verbatim.
func TestSend_preservesCallerTimestamp(t *testing.T) {
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

	const fixedTS int64 = 1700000000000000
	c := New(Config{BaseURL: srv.URL, MaxBatchSize: 100, FlushInterval: 1 * time.Hour})
	c.Send(context.Background(), "preserve_test", map[string]any{"_timestamp": fixedTS, "k": "v"})
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("received %d events, want 1", len(received))
	}
	ts, _ := received[0]["_timestamp"].(float64)
	if int64(ts) != fixedTS {
		t.Errorf("caller-supplied _timestamp overwritten: got %v, want %d", ts, fixedTS)
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
