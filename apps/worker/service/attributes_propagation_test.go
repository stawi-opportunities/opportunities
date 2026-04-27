package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/cache"
	"github.com/pitabwire/frame/frametests"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/kv"
)

// collectorTest is a minimal in-memory event handler used to keep
// the in-memory queue alive while a unit-under-test emits.
type collectorTest struct {
	topic string
}

func (c *collectorTest) Name() string { return c.topic }
func (c *collectorTest) PayloadType() any {
	var raw json.RawMessage
	return &raw
}
func (c *collectorTest) Validate(_ context.Context, _ any) error  { return nil }
func (c *collectorTest) Execute(_ context.Context, _ any) error   { return nil }

// TestNormalize_PreservesAttributes verifies that the per-kind
// Attributes map flows through the normalize stage with well-known
// string fields trimmed/lowercased and the universal envelope
// mirrored as attribute keys.
func TestNormalize_PreservesAttributes(t *testing.T) {
	in := eventsv1.VariantIngestedV1{
		VariantID:     "v1",
		HardKey:       "src|ext",
		Kind:          "scholarship",
		Title:         "MSc Climate",
		IssuingEntity: "ACF",
		AnchorCountry: "ke",
		Attributes: map[string]any{
			"field_of_study": "Climate",
			"degree_level":   "masters",
			"language":       " EN ",
		},
	}

	out := normalize(in)

	if out.Kind != "scholarship" {
		t.Errorf("kind: got %q", out.Kind)
	}
	if out.Attributes["field_of_study"] != "Climate" {
		t.Errorf("field_of_study lost: %v", out.Attributes["field_of_study"])
	}
	if out.Attributes["degree_level"] != "masters" {
		t.Errorf("degree_level lost: %v", out.Attributes["degree_level"])
	}
	if out.Attributes["language"] != "en" {
		t.Errorf("language not lowercased/trimmed: %v", out.Attributes["language"])
	}
	if out.Attributes["title"] != "MSc Climate" {
		t.Errorf("title not mirrored: %v", out.Attributes["title"])
	}
	if out.Attributes["issuing_entity"] != "ACF" {
		t.Errorf("issuing_entity not mirrored: %v", out.Attributes["issuing_entity"])
	}
	if out.Attributes["country"] != "KE" {
		t.Errorf("country not uppercased: %v", out.Attributes["country"])
	}
}

// TestDedup_StoresAttributesInSnapshot verifies that the dedup
// handler writes the inbound VariantValidatedV1.Attributes into the
// cluster snapshot it manages, so the canonical-merge stage finds
// them on its read.
func TestDedup_StoresAttributesInSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

	// Init wires the events publisher so dedup's Emit downstream
	// to TopicVariantsClustered does not fail with
	// "publisher is not initialized". We register a no-op subscriber
	// for the clustered topic so the in-memory queue accepts the
	// publish.
	colClustered := &collectorTest{topic: eventsv1.TopicVariantsClustered}
	svc.EventsManager().Add(colClustered)
	svc.Init(ctx)
	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond)

	h := NewDedupHandlerWithCluster(svc, dedupCache, clusterCache)

	val := eventsv1.VariantValidatedV1{
		VariantID:   "v1",
		HardKey:     "src|ext",
		Kind:        "scholarship",
		Valid:       true,
		ValidatedAt: time.Now().UTC(),
		Attributes: map[string]any{
			"field_of_study": "Climate",
			"degree_level":   "masters",
		},
	}
	env := eventsv1.NewEnvelope(eventsv1.TopicVariantsValidated, val)
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	rm := json.RawMessage(raw)

	if err := h.Execute(ctx, &rm); err != nil {
		t.Fatalf("dedup execute: %v", err)
	}

	clusterID, hit, err := dedupCache.Get(ctx, val.HardKey)
	if err != nil {
		t.Fatalf("dedup cache get: %v", err)
	}
	if !hit {
		t.Fatalf("dedup cache miss after Execute")
	}

	snap, hit, err := clusterCache.Get(ctx, clusterID)
	if err != nil {
		t.Fatalf("cluster cache get: %v", err)
	}
	if !hit {
		t.Fatalf("cluster snapshot not stored by dedup")
	}
	if snap.Kind != "scholarship" {
		t.Errorf("snap.Kind: got %q", snap.Kind)
	}
	if got := snap.Attributes["field_of_study"]; got != "Climate" {
		t.Errorf("snap.Attributes[field_of_study]: got %v, want Climate", got)
	}
	if got := snap.Attributes["degree_level"]; got != "masters" {
		t.Errorf("snap.Attributes[degree_level]: got %v, want masters", got)
	}
}

// TestMergeAttributes_NewerWinsButEmptyDoesNotBlank confirms the
// merge helper's behaviour: pre-existing keys are preserved when the
// newer map is missing them or has empty/nil values, and non-empty
// new values overwrite.
func TestMergeAttributes_NewerWinsButEmptyDoesNotBlank(t *testing.T) {
	prev := map[string]any{
		"field_of_study": "Climate",
		"degree_level":   "masters",
		"language":       "en",
	}
	next := map[string]any{
		"degree_level": "phd",   // overwrites
		"language":     "",      // ignored — does not blank
		"new_key":      "value", // added
	}

	merged := mergeAttributes(prev, next)

	if merged["field_of_study"] != "Climate" {
		t.Errorf("field_of_study lost: %v", merged["field_of_study"])
	}
	if merged["degree_level"] != "phd" {
		t.Errorf("degree_level not overwritten: %v", merged["degree_level"])
	}
	if merged["language"] != "en" {
		t.Errorf("empty new value blanked existing: %v", merged["language"])
	}
	if merged["new_key"] != "value" {
		t.Errorf("new_key not added: %v", merged["new_key"])
	}
}
