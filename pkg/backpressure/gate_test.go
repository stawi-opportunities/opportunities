package backpressure

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fixtureBody builds a minimal /jsz JSON with one consumer at pending.
func fixtureBody(t *testing.T, stream, consumer string, pending int) []byte {
	t.Helper()
	body, err := json.Marshal(jsz{
		AccountDetails: []jszAccount{{
			Name: "opportunities/root-account",
			StreamDetail: []jszStream{{
				Name: stream,
				ConsumerDetail: []jszConsumer{{
					Name:       consumer,
					NumPending: pending,
				}},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// servePending returns a test server that emits a /jsz body with the
// requested pending count on every call.
func servePending(t *testing.T, pending *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixtureBody(t, "svc_opportunities_events", "crawler-events", *pending))
	}))
}

func TestParsePending_happyPath(t *testing.T) {
	body := fixtureBody(t, "my_stream", "my_consumer", 42)
	got, err := parsePending(body, "my_stream", "my_consumer")
	if err != nil {
		t.Fatal(err)
	}
	if got != 42 {
		t.Errorf("got %d want 42", got)
	}
}

func TestParsePending_wrongStream(t *testing.T) {
	body := fixtureBody(t, "some_other", "my_consumer", 42)
	if _, err := parsePending(body, "my_stream", "my_consumer"); err == nil {
		t.Error("expected not-found error for wrong stream")
	}
}

func TestParsePending_wrongConsumer(t *testing.T) {
	body := fixtureBody(t, "my_stream", "other_consumer", 42)
	if _, err := parsePending(body, "my_stream", "my_consumer"); err == nil {
		t.Error("expected not-found error for wrong consumer")
	}
}

// Open below the high-water → paused=false.
func TestGate_openBelowHighWater(t *testing.T) {
	pending := 50_000
	srv := servePending(t, &pending)
	defer srv.Close()

	g := New(Config{
		MonitorURL:   srv.URL,
		StreamName:   "svc_opportunities_events",
		ConsumerName: "crawler-events",
		HighWater:    100_000,
		LowWater:     50_000,
		CacheTTL:     time.Nanosecond, // effectively disabled for tests
	}, nil)

	s, err := g.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.Paused {
		t.Errorf("gate paused at pending=%d below high=%d", s.Pending, s.HighWater)
	}
	if s.Pending != 50_000 {
		t.Errorf("pending=%d want 50_000", s.Pending)
	}
}

// At and above high-water → paused=true.
func TestGate_pausedAtHighWater(t *testing.T) {
	pending := 100_500
	srv := servePending(t, &pending)
	defer srv.Close()

	g := New(Config{
		MonitorURL:   srv.URL,
		StreamName:   "svc_opportunities_events",
		ConsumerName: "crawler-events",
		HighWater:    100_000,
		LowWater:     50_000,
		CacheTTL:     time.Nanosecond,
	}, nil)

	s, err := g.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !s.Paused {
		t.Errorf("gate not paused at pending=%d >= high=%d", s.Pending, s.HighWater)
	}
}

// Hysteresis: once saturated, stays saturated until pending < low-water.
func TestGate_hysteresis(t *testing.T) {
	pending := 100_500
	srv := servePending(t, &pending)
	defer srv.Close()

	g := New(Config{
		MonitorURL:   srv.URL,
		StreamName:   "svc_opportunities_events",
		ConsumerName: "crawler-events",
		HighWater:    100_000,
		LowWater:     50_000,
		CacheTTL:     time.Nanosecond,
	}, nil)

	// Step 1: saturate.
	if s, _ := g.Check(context.Background()); !s.Paused {
		t.Fatal("expected paused on initial saturation")
	}

	// Step 2: drop to 75k — still above low-water, must stay paused.
	pending = 75_000
	if s, _ := g.Check(context.Background()); !s.Paused {
		t.Errorf("expected paused at 75k (above low=%d)", g.lowWater)
	}

	// Step 3: fall below low-water → re-open.
	pending = 49_999
	if s, _ := g.Check(context.Background()); s.Paused {
		t.Errorf("expected open at 49_999 (below low=%d)", g.lowWater)
	}

	// Step 4: climb back to 75k — must stay open until high-water again.
	pending = 75_000
	if s, _ := g.Check(context.Background()); s.Paused {
		t.Errorf("expected open at 75k after hysteresis reset")
	}
}

// Degenerate config: LowWater >= HighWater is clamped to Hi-1.
func TestGate_clampsBadHysteresis(t *testing.T) {
	g := New(Config{
		MonitorURL:   "http://invalid",
		StreamName:   "s",
		ConsumerName: "c",
		HighWater:    100,
		LowWater:     200, // nonsense; should clamp
	}, nil)
	if g.lowWater >= g.highWater {
		t.Errorf("lowWater %d not clamped below highWater %d", g.lowWater, g.highWater)
	}
}

// Empty MonitorURL → gate always reports open (permissive fallback
// when backpressure isn't configured).
func TestGate_emptyURLPermits(t *testing.T) {
	g := New(Config{}, nil)
	s, err := g.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.Paused {
		t.Error("gate must not pause when monitor URL unset")
	}
}

// NATS monitor 500 → fail-open (let work through) + non-nil error for
// operator visibility.
func TestGate_monitorErrorFailsOpen(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	g := New(Config{
		MonitorURL:   srv.URL,
		StreamName:   "svc_opportunities_events",
		ConsumerName: "crawler-events",
		HighWater:    100_000,
		CacheTTL:     time.Nanosecond,
	}, nil)

	s, err := g.Check(context.Background())
	if err == nil {
		t.Fatal("expected error surfaced from monitor 500")
	}
	if s.Paused {
		t.Error("gate must fail-open on monitor error, not fail-closed")
	}
}

// Cache TTL: two calls within TTL only hit NATS once.
func TestGate_cachesWithinTTL(t *testing.T) {
	pending := 42
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixtureBody(t, "s", "c", pending))
	}))
	defer srv.Close()

	g := New(Config{
		MonitorURL:   srv.URL,
		StreamName:   "s",
		ConsumerName: "c",
		HighWater:    100,
		CacheTTL:     1 * time.Hour,
	}, nil)

	_, _ = g.Check(context.Background())
	_, _ = g.Check(context.Background())
	_, _ = g.Check(context.Background())
	if calls != 1 {
		t.Errorf("expected 1 NATS call within TTL, got %d", calls)
	}
}

// Nil receiver must not panic — callers use the gate unconditionally.
func TestGate_nilReceiver(t *testing.T) {
	var g *Gate
	s, err := g.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.Paused {
		t.Error("nil gate must report open, got paused")
	}
}

func TestAdmitOpenGrantsFullWant(t *testing.T) {
	// Construct a gate with a stub monitor that reports a small pending
	// count (well below LowWater). Admit should grant the full amount
	// requested.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"account_details":[{"name":"test","stream_detail":[{"name":"svc","consumer_detail":[{"name":"c","num_pending":10}]}]}]}`))
	}))
	defer srv.Close()

	g := New(Config{
		MonitorURL:   srv.URL,
		StreamName:   "svc",
		ConsumerName: "c",
		HighWater:    100,
		LowWater:     50,
		CacheTTL:     time.Nanosecond,
	}, srv.Client())

	granted, wait := g.Admit(context.Background(), "crawl.requests.v1", 20)
	if granted != 20 {
		t.Fatalf("granted=%d, want 20", granted)
	}
	if wait != 0 {
		t.Fatalf("wait=%v, want 0", wait)
	}
}

func TestAdmitPausedGrantsZero(t *testing.T) {
	// Stub monitor reports pending above HighWater; gate flips saturated.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"account_details":[{"name":"test","stream_detail":[{"name":"svc","consumer_detail":[{"name":"c","num_pending":200}]}]}]}`))
	}))
	defer srv.Close()

	g := New(Config{
		MonitorURL:   srv.URL,
		StreamName:   "svc",
		ConsumerName: "c",
		HighWater:    100,
		LowWater:     50,
		CacheTTL:     time.Nanosecond,
	}, srv.Client())

	granted, wait := g.Admit(context.Background(), "crawl.requests.v1", 20)
	if granted != 0 {
		t.Fatalf("granted=%d, want 0", granted)
	}
	if wait != 30*time.Second {
		t.Fatalf("wait=%v, want 30s", wait)
	}
}

func TestAdmitNoMonitorAlwaysOpen(t *testing.T) {
	// A gate with no monitor URL should always return the full amount
	// (fail-open matches the existing Check() behaviour).
	g := New(Config{HighWater: 100, LowWater: 50}, nil)
	granted, wait := g.Admit(context.Background(), "anything.v1", 5)
	if granted != 5 {
		t.Fatalf("granted=%d, want 5", granted)
	}
	if wait != 0 {
		t.Fatalf("wait=%v, want 0", wait)
	}
}

func TestAdmit_FullPolicy(t *testing.T) {
	g := New(Config{MonitorURL: ""}, nil)

	g.ConfigTopic("opportunities.variants.validated.v1", Policy{
		MaxDrainTime:     15 * time.Minute,
		HardCeilingDrain: 30 * time.Minute,
		HPACeilingKnown:  true,
	})

	// Fast drain: full grant (10s drain << 15m MaxDrainTime).
	g.UpdateLag("opportunities.variants.validated.v1", 1000, 100.0, false)
	got, wait := g.Admit(context.Background(), "opportunities.variants.validated.v1", 50)
	require.Equal(t, 50, got)
	require.Zero(t, wait)

	// Mid-throttle: ~50% grant at 22.5-minute drain.
	// depth=1350, rate=1.0 → drain=1350s ≈ 22.5m
	// fraction = 1 - (22.5-15)/(30-15) = 1 - 7.5/15 = 0.5 → ~50 of 100.
	g.UpdateLag("opportunities.variants.validated.v1", 1350, 1.0, false)
	got, wait = g.Admit(context.Background(), "opportunities.variants.validated.v1", 100)
	require.InDelta(t, 50, got, 5)
	require.Greater(t, wait, time.Duration(0))

	// Above hard ceiling: admit=0.
	// depth=10_000, rate=1.0 → drain=10000s ≈ 166m >> 30m HardCeiling.
	g.UpdateLag("opportunities.variants.validated.v1", 10_000, 1.0, false)
	got, wait = g.Admit(context.Background(), "opportunities.variants.validated.v1", 100)
	require.Equal(t, 0, got)
	require.Greater(t, wait, time.Duration(0))

	// HPA at ceiling collapses window: drain=1200s=20m > MaxDrainTime=15m → 0.
	g.UpdateLag("opportunities.variants.validated.v1", 1200, 1.0, true)
	got, wait = g.Admit(context.Background(), "opportunities.variants.validated.v1", 100)
	require.Equal(t, 0, got)
	require.Greater(t, wait, time.Duration(0))
}

func TestAdmit_UnconfiguredTopic_FailOpen(t *testing.T) {
	g := New(Config{MonitorURL: ""}, nil)
	got, wait := g.Admit(context.Background(), "opportunities.variants.ingested.v1", 42)
	require.Equal(t, 42, got)
	require.Zero(t, wait)
}
