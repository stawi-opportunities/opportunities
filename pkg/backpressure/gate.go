// Package backpressure implements a queue-depth gate for crawl
// dispatch. When the crawler's NATS consumer has more than HighWater
// messages pending, the gate paints the pipeline as "saturated" and
// new work is refused. It re-opens only once pending drops below
// LowWater — hysteresis prevents flapping at the threshold.
//
// Gate lives in-process; every admin-endpoint call re-reads the NATS
// monitor so there's no coordination between pods. With a 100k /
// 50k range and a 1m Trustage poll cadence, the occasional
// out-of-sync read between the three crawler replicas is noise —
// worst case, 3 concurrent "not-saturated" reads might collectively
// dispatch a few hundred extra sources before the gate kicks in.
package backpressure

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// Gate tracks the saturated/open state with hysteresis. Instance is
// safe for concurrent use from any number of HTTP handlers.
type Gate struct {
	monitorURL  string
	streamName  string
	consumerName string
	highWater   int
	lowWater    int
	httpClient  *http.Client
	cache       atomic.Pointer[cachedDepth]
	cacheTTL    time.Duration

	// saturated reflects the current open/closed state. Loads + stores
	// are atomic so the Trustage-facing `paused` flag in JSON responses
	// is consistent without a mutex.
	saturated atomic.Bool
}

type cachedDepth struct {
	pending int
	at      time.Time
}

// Config knobs for the gate. Zero values default to sane production
// values; ranges outside the documented limits are silently clamped.
type Config struct {
	// MonitorURL is the NATS http monitor base (port 8222). Typically
	// http://core-queue-headless.queue-system.svc.cluster.local:8222
	MonitorURL string

	// StreamName matches the JetStream stream feeding the crawler
	// (e.g. svc_stawi_jobs_events). ConsumerName matches the durable
	// consumer (e.g. crawler-events).
	StreamName   string
	ConsumerName string

	// HighWater is the pause threshold. Must be >= LowWater + 1, else
	// hysteresis is disabled (saturated/open flips on every sample).
	HighWater int
	LowWater  int

	// CacheTTL controls how often NATS is polled. The monitor endpoint
	// is very cheap but hitting it on every request is wasteful at
	// 1m Trustage cadence. 10s keeps responses fresh without spamming.
	CacheTTL time.Duration

	// HTTPTimeout bounds the monitor poll. Short because the only
	// reason it'd hang is a NATS outage, and in that case we want to
	// fail fast + err on the side of "open".
	HTTPTimeout time.Duration
}

// New constructs a Gate from cfg. Pass an already-configured
// http.Client if the caller has shared infrastructure (Frame HTTP
// client manager, etc.); nil creates a default internal client.
func New(cfg Config, httpClient *http.Client) *Gate {
	if cfg.HighWater <= 0 {
		cfg.HighWater = 100_000
	}
	if cfg.LowWater <= 0 {
		cfg.LowWater = cfg.HighWater / 2
	}
	if cfg.LowWater >= cfg.HighWater {
		// degenerate configuration — tolerate by forcing at least
		// 1-msg hysteresis so the gate doesn't oscillate.
		cfg.LowWater = cfg.HighWater - 1
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = 10 * time.Second
	}
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = 5 * time.Second
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: cfg.HTTPTimeout}
	}
	return &Gate{
		monitorURL:   strings.TrimRight(cfg.MonitorURL, "/"),
		streamName:   cfg.StreamName,
		consumerName: cfg.ConsumerName,
		highWater:    cfg.HighWater,
		lowWater:     cfg.LowWater,
		httpClient:   httpClient,
		cacheTTL:     cfg.CacheTTL,
	}
}

// State snapshots the gate's public state for response bodies.
type State struct {
	Paused    bool `json:"paused"`
	Pending   int  `json:"pending"`
	HighWater int  `json:"high_water"`
	LowWater  int  `json:"low_water"`
}

// Check evaluates the gate against the current queue depth. Returns
// the snapshot State and a nil error on success. A non-nil error
// means the gate couldn't read depth — callers should treat that as
// fail-open (let the work through) so a NATS outage doesn't wedge the
// dispatch path. When cfg is zero-value (no monitor URL), Check
// returns a permanently-open state.
func (g *Gate) Check(ctx context.Context) (State, error) {
	if g == nil || g.monitorURL == "" {
		return State{HighWater: g.highWaterSafe(), LowWater: g.lowWaterSafe()}, nil
	}

	pending, err := g.readPending(ctx)
	if err != nil {
		// Fail open: let the caller proceed. The operator sees the
		// error in logs, and the gate doesn't become a SPOF on NATS.
		return State{
			Paused:    g.saturated.Load(),
			Pending:   -1,
			HighWater: g.highWater,
			LowWater:  g.lowWater,
		}, err
	}

	// Hysteresis transitions — LOAD-STORE isn't atomic vs. other
	// goroutines, but the correctness here is loose: two concurrent
	// crossings near the threshold either both flip us into saturated
	// (idempotent) or both keep us out (also fine).
	switch {
	case pending >= g.highWater:
		g.saturated.Store(true)
	case pending < g.lowWater:
		g.saturated.Store(false)
	}

	return State{
		Paused:    g.saturated.Load(),
		Pending:   pending,
		HighWater: g.highWater,
		LowWater:  g.lowWater,
	}, nil
}

// readPending queries NATS's monitor API for this gate's consumer and
// returns its num_pending. Cached by cacheTTL because the monitor
// traverses every stream's metadata per call and we don't need live
// sub-second accuracy here.
func (g *Gate) readPending(ctx context.Context) (int, error) {
	if c := g.cache.Load(); c != nil && time.Since(c.at) < g.cacheTTL {
		return c.pending, nil
	}

	url := fmt.Sprintf("%s/jsz?streams=true&consumers=true&accounts=true", g.monitorURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("backpressure: build request: %w", err)
	}
	resp, err := g.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("backpressure: query nats monitor: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("backpressure: nats monitor status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return 0, fmt.Errorf("backpressure: read monitor body: %w", err)
	}
	pending, err := parsePending(body, g.streamName, g.consumerName)
	if err != nil {
		return 0, err
	}
	g.cache.Store(&cachedDepth{pending: pending, at: time.Now()})
	return pending, nil
}

// parsePending walks the /jsz JSON tree looking for the named
// consumer's num_pending. Split out from readPending so tests don't
// need a live NATS.
func parsePending(body []byte, streamName, consumerName string) (int, error) {
	var doc jsz
	if err := json.Unmarshal(body, &doc); err != nil {
		return 0, fmt.Errorf("backpressure: parse monitor json: %w", err)
	}
	for _, acct := range doc.AccountDetails {
		for _, st := range acct.StreamDetail {
			if st.Name != streamName {
				continue
			}
			for _, c := range st.ConsumerDetail {
				if c.Name == consumerName {
					return c.NumPending, nil
				}
			}
		}
	}
	return 0, fmt.Errorf("backpressure: consumer %q not found on stream %q", consumerName, streamName)
}

// jsz mirrors only the fields we care about. Other fields are
// ignored during unmarshal.
type jsz struct {
	AccountDetails []jszAccount `json:"account_details"`
}
type jszAccount struct {
	Name         string      `json:"name"`
	StreamDetail []jszStream `json:"stream_detail"`
}
type jszStream struct {
	Name           string        `json:"name"`
	ConsumerDetail []jszConsumer `json:"consumer_detail"`
}
type jszConsumer struct {
	Name       string `json:"name"`
	NumPending int    `json:"num_pending"`
}

func (g *Gate) highWaterSafe() int {
	if g == nil {
		return 0
	}
	return g.highWater
}
func (g *Gate) lowWaterSafe() int {
	if g == nil {
		return 0
	}
	return g.lowWater
}
