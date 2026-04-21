package service_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/cache"
	"github.com/pitabwire/frame/frametests"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/kv"

	workersvc "stawi.jobs/apps/worker/service"
)

// collector subscribes to a single topic on the in-memory Frame pub/sub and
// accumulates received raw message bytes. Used to assert downstream emission
// in the pipeline test without performing any further processing.
type collector struct {
	topic string
	mu    sync.Mutex
	got   []json.RawMessage
}

func (c *collector) Name() string { return c.topic }
func (c *collector) PayloadType() any {
	var raw json.RawMessage
	return &raw
}
func (c *collector) Validate(_ context.Context, _ any) error { return nil }
func (c *collector) Execute(_ context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	c.mu.Lock()
	c.got = append(c.got, append(json.RawMessage(nil), *raw...))
	c.mu.Unlock()
	return nil
}
func (c *collector) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.got)
}

// TestWorkerPipelineE2E verifies the full normalize → validate → dedup →
// canonical chain using Frame's in-memory pub/sub and in-memory cache.
// No external services are required.
//
// The canonicalFanout handler (embed/translate/publish) is intentionally
// excluded from this test to avoid a Name-keyed registry collision with the
// canonical collector registered on TopicCanonicalsUpserted. Embed, translate,
// and publish are all no-ops when extractor/publisher are nil anyway.
func TestWorkerPipelineE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Frame service with in-memory cache ("worker") and a no-op HTTP
	// driver (avoids binding a real port in CI). The default events queue
	// URL ("mem://…") gives us the in-memory pub/sub bus for free.
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithCacheManager(),
		frame.WithInMemoryCache("worker"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	// Typed cache views — same keyFuncs as main.go.
	dedupCache, ok := cache.GetCache[string, string](
		svc.CacheManager(), "worker",
		func(k string) string { return "dedup:" + k },
	)
	if !ok {
		t.Fatal("dedup cache not wired")
	}
	clusterCache, ok := cache.GetCache[string, kv.ClusterSnapshot](
		svc.CacheManager(), "worker",
		func(k string) string { return "cluster:" + k },
	)
	if !ok {
		t.Fatal("cluster cache not wired")
	}

	// Build the service. We pass nil extractor (validate fail-opens with
	// score=0.5) and nil publisher (publish handler is a no-op).
	wsvc := workersvc.NewService(svc, nil, nil, dedupCache, clusterCache, nil)

	// Register a collector on TopicCanonicalsUpserted BEFORE the pipeline
	// handlers so we can observe the canonical-merge output. We only
	// register Handlers()[:4] — normalize, validate, dedup, canonical —
	// which leaves TopicCanonicalsUpserted free for the collector.
	// The canonicalFanout (Handlers()[4]) is skipped; with nil
	// extractor/publisher its embed/translate/publish stages are no-ops.
	colCanonical := &collector{topic: eventsv1.TopicCanonicalsUpserted}
	svc.EventsManager().Add(colCanonical)

	// Register the four pipeline handlers via WithRegisterEvents so they
	// are added during the pre-start phase (after eventsManager is wired
	// to the live queue). handlers[:4] = normalize, validate, dedup, canonical.
	handlers := wsvc.Handlers()
	svc.Init(ctx, frame.WithRegisterEvents(handlers[:4]...))

	// Start Frame's run-loop in the background. The pre-start phase
	// executes here, registering all four pipeline handlers. The
	// collector was added directly before Init so it is not overwritten.
	go func() { _ = svc.Run(ctx, "") }()

	// Give the subscriber goroutine time to start consuming from the
	// in-memory queue before we emit.
	time.Sleep(300 * time.Millisecond)

	// Emit the seed event. Country is lowercase "ke" so we can assert
	// that normalize uppercased it to "KE" on the cluster snapshot.
	now := time.Now().UTC()
	in := eventsv1.NewEnvelope(eventsv1.TopicVariantsIngested, eventsv1.VariantIngestedV1{
		VariantID:  "var_pipe_1",
		SourceID:   "src_pipe",
		ExternalID: "ext_1",
		HardKey:    "src_pipe|ext_1",
		Stage:      "ingested",
		Title:      "Backend Engineer",
		Company:    "Acme",
		Country:    "ke", // lowercase — normalize should uppercase this
		RemoteType: "",
		ScrapedAt:  now,
		PostedAt:   now,
	})
	if err := svc.EventsManager().Emit(ctx, eventsv1.TopicVariantsIngested, in); err != nil {
		t.Fatalf("emit seed event: %v", err)
	}

	// Poll until the canonical collector receives an event (pipeline settled).
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if colCanonical.Len() > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// --- Assertions ---

	if colCanonical.Len() == 0 {
		t.Fatal("pipeline did not produce a CanonicalUpsertedV1 event within timeout")
	}

	// Decode the canonical event and assert required fields.
	colCanonical.mu.Lock()
	rawCanonical := colCanonical.got[0]
	colCanonical.mu.Unlock()

	var canonicalEnv eventsv1.Envelope[eventsv1.CanonicalUpsertedV1]
	if err := json.Unmarshal(rawCanonical, &canonicalEnv); err != nil {
		t.Fatalf("decode canonical envelope: %v", err)
	}
	c := canonicalEnv.Payload
	if c.CanonicalID == "" {
		t.Fatalf("canonical_id is empty: %+v", c)
	}
	if c.ClusterID == "" {
		t.Fatalf("cluster_id is empty: %+v", c)
	}

	// Verify the cluster cache holds the snapshot with the normalised country.
	snap, hit, err := clusterCache.Get(ctx, c.ClusterID)
	if err != nil {
		t.Fatalf("clusterCache.Get(%q): %v", c.ClusterID, err)
	}
	if !hit {
		t.Fatalf("no cluster snapshot found for cluster_id=%q", c.ClusterID)
	}
	if snap.Country != "KE" {
		t.Fatalf("cluster snapshot country not normalized: got %q, want %q", snap.Country, "KE")
	}
	if snap.CanonicalID == "" {
		t.Fatalf("cluster snapshot missing canonical_id")
	}
}
