// Package analytics ships structured events to OpenObserve via its HTTP
// ingest API. We use it for user-facing telemetry (job views, apply
// clicks, engagement) rather than application logs — those still go to the
// OpenTelemetry collector. The split keeps product analytics queryable as
// SQL in its own streams while ops telemetry stays in otlp-backend.
package analytics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Client is an OpenObserve ingest client. It batches events in memory and
// flushes either when the batch reaches MaxBatchSize or when FlushInterval
// elapses — whichever comes first. The batching matters because each ingest
// request has non-trivial overhead (auth, TLS handshake, JSON framing), so
// hitting OpenObserve on every view would waste both CPU and connection
// churn under even moderate traffic.
//
// Zero-value and nil Clients are safe: Send is a no-op, which lets callers
// run without analytics configured in local dev.
type Client struct {
	baseURL  string // e.g. "http://openobserve-openobserve-standalone.telemetry.svc:5080"
	org      string // OpenObserve organisation, usually "default"
	username string // Basic-auth email
	password string // Basic-auth password/token
	http     *http.Client

	maxBatch int
	flushInt time.Duration

	mu      sync.Mutex
	buckets map[string][]map[string]any // stream → pending events
	stopCh  chan struct{}
	stopped bool
	wg      sync.WaitGroup
}

// Config carries the fields pulled from env/Vault. An empty BaseURL turns
// the client into a silent no-op — makes it easy to run services locally
// without standing up OpenObserve.
type Config struct {
	BaseURL       string
	Org           string
	Username      string
	Password      string
	MaxBatchSize  int           // 0 → 100
	FlushInterval time.Duration // 0 → 2s
	HTTPTimeout   time.Duration // 0 → 10s
}

// New constructs a Client and starts its background flusher. Return nil
// when cfg.BaseURL is empty so callers can treat it as "no-op" without
// branching on config.
func New(cfg Config) *Client {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil
	}
	if cfg.Org == "" {
		cfg.Org = "default"
	}
	if cfg.MaxBatchSize <= 0 {
		cfg.MaxBatchSize = 100
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 2 * time.Second
	}
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = 10 * time.Second
	}

	c := &Client{
		baseURL:  strings.TrimRight(cfg.BaseURL, "/"),
		org:      cfg.Org,
		username: cfg.Username,
		password: cfg.Password,
		http:     &http.Client{Timeout: cfg.HTTPTimeout},
		maxBatch: cfg.MaxBatchSize,
		flushInt: cfg.FlushInterval,
		buckets:  make(map[string][]map[string]any),
		stopCh:   make(chan struct{}),
	}
	c.wg.Add(1)
	go c.runFlusher()
	return c
}

// Send enqueues an event for the named stream. The event is a flat JSON
// object; OpenObserve indexes every key. If the batch for this stream hits
// MaxBatchSize, Send triggers an immediate flush of that stream (other
// streams keep buffering). Returns silently when c is nil.
func (c *Client) Send(ctx context.Context, stream string, event map[string]any) {
	if c == nil || c.stopped || stream == "" || len(event) == 0 {
		return
	}
	// Always stamp a canonical timestamp. OpenObserve requires _timestamp
	// in microseconds; using UnixMicro keeps the events ordered even when
	// the caller forgets to set anything.
	if _, ok := event["_timestamp"]; !ok {
		event["_timestamp"] = time.Now().UnixMicro()
	}

	c.mu.Lock()
	c.buckets[stream] = append(c.buckets[stream], event)
	shouldFlush := len(c.buckets[stream]) >= c.maxBatch
	batch := c.buckets[stream]
	if shouldFlush {
		c.buckets[stream] = nil
	}
	c.mu.Unlock()

	if shouldFlush {
		// Fire off the full batch synchronously-but-goroutined so Send
		// never blocks the caller. The flusher-goroutine path handles
		// partial batches on the timer.
		go func(stream string, batch []map[string]any) {
			_ = c.flush(ctx, stream, batch)
		}(stream, batch)
	}
}

// Close drains any remaining buffered events and stops the flusher. Safe
// to call on a nil Client (no-op).
func (c *Client) Close(ctx context.Context) error {
	if c == nil || c.stopped {
		return nil
	}
	c.stopped = true
	close(c.stopCh)
	c.wg.Wait()

	// Final drain.
	c.mu.Lock()
	pending := c.buckets
	c.buckets = nil
	c.mu.Unlock()

	var firstErr error
	for stream, batch := range pending {
		if err := c.flush(ctx, stream, batch); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// runFlusher periodically drains all buckets. Ticks are cheap — if nothing
// is buffered the loop iteration exits before touching the network.
func (c *Client) runFlusher() {
	defer c.wg.Done()
	t := time.NewTicker(c.flushInt)
	defer t.Stop()
	for {
		select {
		case <-c.stopCh:
			return
		case <-t.C:
			c.flushAll()
		}
	}
}

func (c *Client) flushAll() {
	c.mu.Lock()
	snapshot := c.buckets
	c.buckets = make(map[string][]map[string]any, len(snapshot))
	c.mu.Unlock()

	for stream, batch := range snapshot {
		if len(batch) == 0 {
			continue
		}
		// Bounded per-batch timeout so one misbehaving stream can't
		// hold the whole flusher up.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_ = c.flush(ctx, stream, batch)
		cancel()
	}
}

// flush posts a single batch. Errors are non-fatal — analytics loss is
// better than cascading failure into the caller's request path. Callers
// can check the return in tests; production ignores it.
func (c *Client) flush(ctx context.Context, stream string, batch []map[string]any) error {
	if len(batch) == 0 {
		return nil
	}
	body, err := json.Marshal(batch)
	if err != nil {
		return fmt.Errorf("analytics: marshal batch: %w", err)
	}

	url := fmt.Sprintf("%s/api/%s/%s/_json", c.baseURL, c.org, stream)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("analytics: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("analytics: do %s: %w", stream, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("analytics: %s status %d", stream, resp.StatusCode)
	}
	return nil
}

// ErrDisabled is returned by configuration helpers when required fields
// are missing and strict mode is on. Services should treat this as a
// warning rather than a fatal — the client just won't ingest.
var ErrDisabled = errors.New("analytics: disabled (missing BaseURL)")
