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

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/frametest"
	"github.com/stawi-opportunities/opportunities/pkg/kv"

	workersvc "github.com/stawi-opportunities/opportunities/apps/worker/service"
)

// collector subscribes to a single topic on the in-memory Frame pub/sub and
// accumulates received raw message bytes. Used to assert downstream emission
// in the pipeline test without performing any further processing.
type collector struct {
	topic string
	mu    sync.Mutex
	got   []json.RawMessage
}

// Handle implements queue.SubscribeWorker so the collector can tap a
// pipeline Frame Queue subject and accumulate the raw bytes it receives.
func (c *collector) Handle(_ context.Context, _ map[string]string, payload []byte) error {
	c.mu.Lock()
	c.got = append(c.got, append(json.RawMessage(nil), payload...))
	c.mu.Unlock()
	return nil
}
func (c *collector) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.got)
}

// pipeline queue Names used by the in-memory E2E tests.
const (
	qIngested   = "pipeline_ingested"
	qNormalized = "pipeline_normalized"
	qValidated  = "pipeline_validated"
	qClustered  = "pipeline_clustered"
	qCanonical  = "pipeline_canonical"
	qFlagged    = "pipeline_flagged"
)

// wireChain registers the full normalize→validate→dedup→canonical Frame
// Queue chain on the in-memory driver and a collector tapping the canonical
// queue. Publish is intentionally skipped (the tests assert up to canonical).
func wireChain(t *testing.T, ctx context.Context, svc *frame.Service, wsvc *workersvc.Service) *collector {
	t.Helper()
	col := &collector{topic: qCanonical}
	m := func(n string) string { return "mem://" + n }
	svc.Init(ctx,
		frame.WithRegisterPublisher(qIngested, m(qIngested)),
		frame.WithRegisterPublisher(qNormalized, m(qNormalized)),
		frame.WithRegisterPublisher(qValidated, m(qValidated)),
		frame.WithRegisterPublisher(qFlagged, m(qFlagged)),
		frame.WithRegisterPublisher(qClustered, m(qClustered)),
		frame.WithRegisterPublisher(qCanonical, m(qCanonical)),
		frame.WithRegisterSubscriber(qIngested, m(qIngested), wsvc.NormalizeWorker(qNormalized)),
		frame.WithRegisterSubscriber(qNormalized, m(qNormalized), wsvc.ValidateWorker(qValidated, qFlagged)),
		frame.WithRegisterSubscriber(qValidated, m(qValidated), wsvc.DedupWorker(qClustered)),
		frame.WithRegisterSubscriber(qClustered, m(qClustered), wsvc.CanonicalWorker(qCanonical)),
		frame.WithRegisterSubscriber(qCanonical, m(qCanonical), col),
	)
	go func() { _ = svc.Run(ctx, "") }()
	frametest.WaitPublisherReady(t, svc, qIngested, 2*time.Second)
	return col
}

// seedIngested publishes a VariantIngestedV1 onto the ingested queue.
func seedIngested(t *testing.T, ctx context.Context, svc *frame.Service, in eventsv1.VariantIngestedV1) {
	t.Helper()
	body, err := json.Marshal(eventsv1.NewEnvelope(eventsv1.TopicVariantsIngested, in))
	if err != nil {
		t.Fatalf("marshal seed: %v", err)
	}
	if err := svc.QueueManager().Publish(ctx, qIngested, body, nil); err != nil {
		t.Fatalf("publish seed: %v", err)
	}
}

// TestWorkerPipelineE2E verifies the full normalize → validate → dedup →
// canonical chain using Frame's in-memory pub/sub and in-memory cache.
// No external services are required.
//
// The publish handler (handlers[4]) is intentionally excluded to avoid a
// Name-keyed registry collision with the canonical collector registered on
// TopicCanonicalsUpserted. Publish is a no-op when publisher is nil anyway.
// The canonical handler still attempts to publish to the embed/translate
// queue subjects but that fails open with a warn-log when the publishers
// are not registered (which is fine for this test).
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
	wsvc := workersvc.NewService(svc, nil, nil, nil, dedupCache, clusterCache, nil, nil, false, false, "valkey")

	// Register a collector on TopicCanonicalsUpserted BEFORE the pipeline
	// handlers so we can observe the canonical-merge output. We only
	// register Handlers()[:4] — normalize, validate, dedup, canonical —
	// which leaves TopicCanonicalsUpserted free for the collector.
	// The canonicalFanout (Handlers()[4]) is skipped; with nil
	// extractor/publisher its embed/translate/publish stages are no-ops.
	// Wire the linear Frame Queue chain and tap the canonical output.
	colCanonical := wireChain(t, ctx, svc, wsvc)

	// Seed the chain. Country is lowercase "ke" so we can assert normalize
	// uppercased it to "KE" on the cluster snapshot.
	seedIngested(t, ctx, svc, eventsv1.VariantIngestedV1{
		VariantID:     "var_pipe_1",
		SourceID:      "src_pipe",
		ExternalID:    "ext_1",
		HardKey:       "src_pipe|ext_1",
		Kind:          "job",
		Stage:         "ingested",
		Title:         "Backend Engineer",
		IssuingEntity: "Acme",
		AnchorCountry: "ke", // lowercase — normalize should uppercase this
		ScrapedAt:     time.Now().UTC(),
	})

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
	if c.OpportunityID == "" {
		t.Fatalf("opportunity_id is empty: %+v", c)
	}
	if c.Kind != "job" {
		t.Errorf("kind: got %q, want %q", c.Kind, "job")
	}
	if c.Title != "Backend Engineer" {
		t.Errorf("title: got %q, want %q", c.Title, "Backend Engineer")
	}
	if c.IssuingEntity != "Acme" {
		t.Errorf("issuing_entity: got %q, want %q", c.IssuingEntity, "Acme")
	}
	if c.AnchorCountry != "KE" {
		t.Errorf("anchor_country: got %q, want %q (normalize should uppercase)", c.AnchorCountry, "KE")
	}
	if c.Slug == "" {
		t.Errorf("slug is empty")
	}

	// Cluster snapshot should also reflect the merged state.
	snap, hit, err := clusterCache.Get(ctx, c.OpportunityID)
	if err != nil {
		t.Fatalf("cluster cache get: %v", err)
	}
	if !hit {
		t.Fatalf("cluster snapshot missing for opportunity %q", c.OpportunityID)
	}
	if snap.Title != "Backend Engineer" {
		t.Errorf("snap.Title: got %q", snap.Title)
	}
	if snap.Company != "Acme" {
		t.Errorf("snap.Company: got %q", snap.Company)
	}
	if snap.Country != "KE" {
		t.Errorf("snap.Country: got %q", snap.Country)
	}
}

// TestPipeline_ScholarshipPropagatesAttributes drives a scholarship
// variant through the full normalize → validate → dedup → canonical
// pipeline and asserts the per-kind facets (field_of_study, degree_level)
// survive on the emitted CanonicalUpsertedV1.Attributes map. This is
// the regression guard for the Fix #3 attribute-propagation bug.
func TestPipeline_ScholarshipPropagatesAttributes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithCacheManager(),
		frame.WithInMemoryCache("worker"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

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

	wsvc := workersvc.NewService(svc, nil, nil, nil, dedupCache, clusterCache, nil, nil, false, false, "valkey")

	colCanonical := wireChain(t, ctx, svc, wsvc)

	seedIngested(t, ctx, svc, eventsv1.VariantIngestedV1{
		VariantID:     "var_sch_1",
		SourceID:      "src_sch",
		ExternalID:    "ext_sch_1",
		HardKey:       "src_sch|ext_sch_1",
		Kind:          "scholarship",
		Stage:         "ingested",
		Title:         "MSc Climate Resilience Fellowship",
		IssuingEntity: "African Climate Foundation",
		AnchorCountry: "KE",
		Attributes: map[string]any{
			"field_of_study": "Climate",
			"degree_level":   "masters",
		},
		ScrapedAt: time.Now().UTC(),
	})

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if colCanonical.Len() > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if colCanonical.Len() == 0 {
		t.Fatal("pipeline did not produce a CanonicalUpsertedV1 event within timeout")
	}

	colCanonical.mu.Lock()
	rawCanonical := colCanonical.got[0]
	colCanonical.mu.Unlock()

	var env eventsv1.Envelope[eventsv1.CanonicalUpsertedV1]
	if err := json.Unmarshal(rawCanonical, &env); err != nil {
		t.Fatalf("decode canonical envelope: %v", err)
	}
	c := env.Payload

	if c.Kind != "scholarship" {
		t.Errorf("kind: got %q, want %q", c.Kind, "scholarship")
	}
	if c.Title != "MSc Climate Resilience Fellowship" {
		t.Errorf("title: got %q", c.Title)
	}
	if c.IssuingEntity != "African Climate Foundation" {
		t.Errorf("issuing_entity: got %q", c.IssuingEntity)
	}
	if c.Attributes == nil {
		t.Fatalf("attributes nil — Fix #3 propagation broken")
	}
	if got, want := c.Attributes["field_of_study"], "Climate"; got != want {
		t.Errorf("attributes[field_of_study]: got %v, want %v", got, want)
	}
	if got, want := c.Attributes["degree_level"], "masters"; got != want {
		t.Errorf("attributes[degree_level]: got %v, want %v", got, want)
	}
	if c.Slug == "" {
		t.Errorf("slug empty")
	}
}
