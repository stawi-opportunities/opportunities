package eventsv1

import (
	"encoding/json"
	"testing"
	"time"
)

type testPayload struct {
	Value string `json:"value"`
}

func TestNewEnvelopeStampsIDsAndTimestamps(t *testing.T) {
	env := NewEnvelope("test.topic.v1", testPayload{Value: "x"})

	if env.EventID == "" {
		t.Fatal("event_id must be set by NewEnvelope")
	}
	if env.EventType != "test.topic.v1" {
		t.Fatalf("event_type=%q, want test.topic.v1", env.EventType)
	}
	if time.Since(env.OccurredAt) > time.Second {
		t.Fatalf("occurred_at=%v too old", env.OccurredAt)
	}
	if env.SchemaVersion != 1 {
		t.Fatalf("schema_version=%d, want 1", env.SchemaVersion)
	}
}

func TestEnvelopeJSONRoundTrip(t *testing.T) {
	orig := NewEnvelope("test.topic.v1", testPayload{Value: "roundtrip"})
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Envelope[testPayload]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.EventID != orig.EventID {
		t.Fatalf("event_id lost on round-trip: got %q want %q", back.EventID, orig.EventID)
	}
	if back.Payload.Value != "roundtrip" {
		t.Fatalf("payload lost: %+v", back.Payload)
	}
}

func TestPartitionKeyRespectsSourceHint(t *testing.T) {
	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	pk := PartitionKey(TopicVariantsIngested, now, "greenhouse")
	if pk.DT != "2026-04-21" {
		t.Fatalf("DT=%q, want 2026-04-21", pk.DT)
	}
	if pk.Secondary != "greenhouse" {
		t.Fatalf("Secondary=%q, want greenhouse", pk.Secondary)
	}
}

func TestPartitionKeyCanonicalUsesClusterPrefix(t *testing.T) {
	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	pk := PartitionKey(TopicCanonicalsUpserted, now, "abcdef123456")
	if pk.Secondary != "ab" {
		t.Fatalf("Secondary=%q, want ab", pk.Secondary)
	}
}

func TestPartitionObjectPathVariantsLabel(t *testing.T) {
	pk := PartKey{DT: "2026-04-21", Secondary: "greenhouse"}
	got := pk.ObjectPath("variants", "abc123")
	want := "variants/dt=2026-04-21/src=greenhouse/abc123.parquet"
	if got != want {
		t.Fatalf("path=%q, want %q", got, want)
	}
}

func TestPartitionObjectPathCanonicalsLabel(t *testing.T) {
	pk := PartKey{DT: "2026-04-21", Secondary: "ab"}
	got := pk.ObjectPath("canonicals", "xyz789")
	want := "canonicals/dt=2026-04-21/cc=ab/xyz789.parquet"
	if got != want {
		t.Fatalf("path=%q, want %q", got, want)
	}
}

func TestCanonicalUpsertedRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicCanonicalsUpserted, CanonicalUpsertedV1{
		CanonicalID: "can_1",
		ClusterID:   "clu_1",
		Slug:        "senior-backend-engineer-acme-ke",
		Title:       "Senior Backend Engineer",
		Company:     "Acme",
		Country:     "KE",
		RemoteType:  "remote",
		Status:      "active",
		PostedAt:    time.Date(2026, 4, 21, 9, 0, 0, 0, time.UTC),
	})
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Envelope[CanonicalUpsertedV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.CanonicalID != "can_1" || back.Payload.Title != "Senior Backend Engineer" {
		t.Fatalf("round-trip lost fields: %+v", back.Payload)
	}
}

func TestEmbeddingRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicEmbeddings, EmbeddingV1{
		CanonicalID:  "can_1",
		Vector:       []float32{0.1, 0.2, 0.3},
		ModelVersion: "text-embed-3-small",
	})
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Envelope[EmbeddingV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.CanonicalID != "can_1" || len(back.Payload.Vector) != 3 {
		t.Fatalf("round-trip lost fields: %+v", back.Payload)
	}
}

func TestVariantNormalizedRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicVariantsNormalized, VariantNormalizedV1{
		VariantID: "var_1", SourceID: "src_x", HardKey: "src_x|e1",
		Title: "Engineer", Country: "KE", RemoteType: "remote",
	})
	raw, _ := json.Marshal(orig)
	var back Envelope[VariantNormalizedV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.VariantID != "var_1" || back.Payload.Country != "KE" {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestVariantValidatedRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicVariantsValidated, VariantValidatedV1{
		VariantID: "var_1", SourceID: "src_x", ValidationScore: 0.9, ModelVersion: "v1",
	})
	raw, _ := json.Marshal(orig)
	var back Envelope[VariantValidatedV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.VariantID != "var_1" || back.Payload.ValidationScore != 0.9 {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestVariantFlaggedRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicVariantsFlagged, VariantFlaggedV1{
		VariantID: "var_1", Reason: "bad title",
	})
	raw, _ := json.Marshal(orig)
	var back Envelope[VariantFlaggedV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.Reason != "bad title" {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestVariantClusteredRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicVariantsClustered, VariantClusteredV1{
		VariantID: "var_1", ClusterID: "clu_1",
	})
	raw, _ := json.Marshal(orig)
	var back Envelope[VariantClusteredV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.ClusterID != "clu_1" {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestTranslationRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicTranslations, TranslationV1{
		CanonicalID: "can_1", Lang: "sw", TitleTr: "Mhandisi",
		DescriptionTr: "Tunaajiri...", ModelVersion: "v1",
	})
	raw, _ := json.Marshal(orig)
	var back Envelope[TranslationV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.Lang != "sw" || back.Payload.TitleTr != "Mhandisi" {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestPublishedRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicPublished, PublishedV1{
		CanonicalID: "can_1", Slug: "job-slug", R2Version: 3,
	})
	raw, _ := json.Marshal(orig)
	var back Envelope[PublishedV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.Slug != "job-slug" || back.Payload.R2Version != 3 {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestCrawlRequestRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicCrawlRequests, CrawlRequestV1{
		RequestID: "req_1",
		SourceID:  "src_greenhouse_acme",
		URL:       "",
		Cursor:    "",
		Mode:      "auto",
		Attempt:   1,
	})
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Envelope[CrawlRequestV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.SourceID != "src_greenhouse_acme" || back.Payload.Mode != "auto" {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestCrawlPageCompletedRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicCrawlPageCompleted, CrawlPageCompletedV1{
		RequestID:    "req_1",
		SourceID:     "src_1",
		URL:          "https://example.com/jobs",
		HTTPStatus:   200,
		JobsFound:    12,
		JobsEmitted:  11,
		JobsRejected: 1,
		Cursor:       "page=2",
		ErrorCode:    "",
	})
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Envelope[CrawlPageCompletedV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.SourceID != "src_1" || back.Payload.JobsFound != 12 {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestSourceDiscoveredRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicSourcesDiscovered, SourceDiscoveredV1{
		DiscoveredURL: "https://another-board.example/careers",
		Name:          "Another Board",
		Country:       "KE",
		Type:          "generic-html",
		SourceID:      "src_1",
	})
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Envelope[SourceDiscoveredV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.DiscoveredURL != "https://another-board.example/careers" {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestPartitionKeySourcesDiscoveredUsesSourceHint(t *testing.T) {
	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	pk := PartitionKey(TopicSourcesDiscovered, now, "src_origin_1")
	if pk.Secondary != "src_origin_1" {
		t.Fatalf("Secondary=%q, want src_origin_1", pk.Secondary)
	}
}

func TestPartitionObjectPathSourcesDiscoveredLabel(t *testing.T) {
	pk := PartKey{DT: "2026-04-21", Secondary: "src_origin_1"}
	got := pk.ObjectPath("sources_discovered", "xyz789")
	want := "sources_discovered/dt=2026-04-21/src=src_origin_1/xyz789.parquet"
	if got != want {
		t.Fatalf("path=%q, want %q", got, want)
	}
}
