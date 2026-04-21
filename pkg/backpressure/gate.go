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
	"sync"
	"sync/atomic"
	"time"
)

// The gate measures pipeline backpressure via NATS JetStream pending-
// message depth, NOT via Postgres state. With raw HTTP bodies in R2
// (pkg/archive) the DB never holds the blobs that could pressure it;
// queue depth is the only correct signal for "processing is behind".
// See docs/superpowers/specs/2026-04-20-r2-blob-archive-design.md.

// Policy holds per-topic admission knobs for the drain-time gate.
type Policy struct {
	// MaxDrainTime is the drain-time threshold below which the full
	// request is granted. Defaults to 15 minutes when zero.
	MaxDrainTime time.Duration

	// HardCeilingDrain is the drain-time at or above which admit=0.
	// Must be > MaxDrainTime; defaults to MaxDrainTime+15m when zero
	// or not strictly greater.
	HardCeilingDrain time.Duration

	// HPACeilingKnown, when true, collapses the throttle window to
	// zero as soon as the HPA reports it is at its replica ceiling.
	// This turns any drain above MaxDrainTime into an immediate zero-
	// admit so the queue drains before more pods are added.
	HPACeilingKnown bool
}

type topicLag struct {
	depth        int64
	consumeRate  float64
	hpaAtCeiling bool
	updatedAt    time.Time
}

// Gate tracks the saturated/open state with hysteresis. Instance is
// safe for concurrent use from any number of HTTP handlers.
type Gate struct {
	monitorURL   string
	streamName   string
	consumerName string
	highWater    int
	lowWater     int
	httpClient   *http.Client
	cache        atomic.Pointer[cachedDepth]
	cacheTTL     time.Duration

	// saturated reflects the current open/closed state. Loads + stores
	// are atomic so the Trustage-facing `paused` flag in JSON responses
	// is consistent without a mutex.
	saturated atomic.Bool

	// Per-topic policy and lag data (Task 5: drain-time + HPA-ceiling).
	policyMu sync.RWMutex
	policies map[string]Policy
	lags     map[string]topicLag
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

// ConfigTopic registers a drain-time Policy for the given topic. The
// gate applies the policy in Admit once UpdateLag has been called at
// least once for that topic. Topics with no policy fall back to the
// hysteresis gate (back-compat).
func (g *Gate) ConfigTopic(topic string, p Policy) {
	g.policyMu.Lock()
	defer g.policyMu.Unlock()
	if g.policies == nil {
		g.policies = make(map[string]Policy)
	}
	if p.MaxDrainTime <= 0 {
		p.MaxDrainTime = 15 * time.Minute
	}
	if p.HardCeilingDrain <= p.MaxDrainTime {
		p.HardCeilingDrain = p.MaxDrainTime + 15*time.Minute
	}
	g.policies[topic] = p
}

// UpdateLag records a fresh depth+rate sample for topic. depth is the
// number of unconsumed messages, consumeRate is messages/second, and
// hpaAtCeiling signals that the HPA is already at its replica ceiling.
// Calling UpdateLag is optional; topics with no lag data fall back to
// the hysteresis gate (back-compat / fail-open).
func (g *Gate) UpdateLag(topic string, depth int64, consumeRate float64, hpaAtCeiling bool) {
	g.policyMu.Lock()
	defer g.policyMu.Unlock()
	if g.lags == nil {
		g.lags = make(map[string]topicLag)
	}
	g.lags[topic] = topicLag{
		depth:        depth,
		consumeRate:  consumeRate,
		hpaAtCeiling: hpaAtCeiling,
		updatedAt:    time.Now(),
	}
}

// Admit asks to emit `want` events on `topic`. When both a Policy and
// a lag sample exist for the topic, the drain-time algorithm is used:
//
//   - drain < MaxDrainTime              → full grant, wait=0
//   - drain >= HardCeilingDrain         → grant 0, wait=clamp(drain-max, 5m)
//   - between the two thresholds        → linear throttle, wait hint
//   - HPACeilingKnown && hpaAtCeiling   → collapse window (max==hard)
//
// When no policy or lag exists for the topic, Admit falls back to the
// hysteresis gate (Phase 4 back-compat behaviour).
func (g *Gate) Admit(ctx context.Context, topic string, want int) (int, time.Duration) {
	if want <= 0 {
		return 0, 0
	}

	g.policyMu.RLock()
	pol, hasPol := g.policies[topic]
	lag, hasLag := g.lags[topic]
	g.policyMu.RUnlock()

	// Back-compat: no policy/lag → fall back to hysteresis OR fail-open.
	if !hasPol || !hasLag {
		if g == nil || g.monitorURL == "" {
			return want, 0
		}
		state, err := g.Check(ctx)
		if err != nil || !state.Paused {
			return want, 0
		}
		return 0, 30 * time.Second
	}

	if lag.consumeRate <= 0 {
		return want, 0
	}

	drain := time.Duration(float64(lag.depth) / lag.consumeRate * float64(time.Second))

	maxDrain := pol.MaxDrainTime
	hardDrain := pol.HardCeilingDrain
	if pol.HPACeilingKnown && lag.hpaAtCeiling {
		hardDrain = maxDrain
	}

	switch {
	case drain < maxDrain:
		return want, 0
	case drain >= hardDrain:
		return 0, clampWait(drain - maxDrain)
	default:
		span := float64(hardDrain - maxDrain)
		over := float64(drain - maxDrain)
		fraction := 1.0 - (over / span)
		granted := int(float64(want) * fraction)
		if granted < 0 {
			granted = 0
		}
		if granted > want {
			granted = want
		}
		return granted, clampWait(drain - maxDrain)
	}
}

// clampWait bounds a wait hint to [0, 5m].
func clampWait(d time.Duration) time.Duration {
	const max = 5 * time.Minute
	switch {
	case d <= 0:
		return 0
	case d > max:
		return max
	default:
		return d
	}
}
