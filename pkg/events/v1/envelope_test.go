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
		OpportunityID: "opp_1",
		Slug:          "senior-backend-engineer-acme-ke",
		HardKey:       "src_x|abc",
		Kind:          "job",
		Title:         "Senior Backend Engineer",
		IssuingEntity: "Acme",
		ApplyURL:      "https://example.com/apply",
		AnchorCountry: "KE",
		Remote:        true,
		PostedAt:      time.Date(2026, 4, 21, 9, 0, 0, 0, time.UTC),
		Attributes:    map[string]any{"remote_type": "remote"},
		UpsertedAt:    time.Date(2026, 4, 21, 10, 0, 0, 0, time.UTC),
	})
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Envelope[CanonicalUpsertedV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.OpportunityID != "opp_1" || back.Payload.Title != "Senior Backend Engineer" {
		t.Fatalf("round-trip lost fields: %+v", back.Payload)
	}
}

func TestEmbeddingRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicEmbeddings, EmbeddingV1{
		OpportunityID: "opp_1",
		Vector:        []float32{0.1, 0.2, 0.3},
		ModelVersion:  "text-embed-3-small",
	})
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Envelope[EmbeddingV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.OpportunityID != "opp_1" || len(back.Payload.Vector) != 3 {
		t.Fatalf("round-trip lost fields: %+v", back.Payload)
	}
}

func TestVariantNormalizedRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicVariantsNormalized, VariantNormalizedV1{
		VariantID: "var_1", HardKey: "src_x|e1", Kind: "job",
		NormalizedAt: time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC),
		Attributes:   map[string]any{"country": "KE", "remote_type": "remote"},
	})
	raw, _ := json.Marshal(orig)
	var back Envelope[VariantNormalizedV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.VariantID != "var_1" || back.Payload.Attributes["country"] != "KE" {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestVariantValidatedRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicVariantsValidated, VariantValidatedV1{
		VariantID: "var_1", HardKey: "src_x|e1", Kind: "job",
		Valid: true, QualityScore: 0.9,
		ValidatedAt: time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC),
	})
	raw, _ := json.Marshal(orig)
	var back Envelope[VariantValidatedV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.VariantID != "var_1" || back.Payload.QualityScore != 0.9 {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestVariantFlaggedRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicVariantsFlagged, VariantFlaggedV1{
		VariantID: "var_1", Kind: "job", Reason: "bad title",
		FlaggedAt: time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC),
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
		VariantID: "var_1", OpportunityID: "opp_1", Kind: "job",
		ClusteredAt: time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC),
	})
	raw, _ := json.Marshal(orig)
	var back Envelope[VariantClusteredV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.OpportunityID != "opp_1" {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestTranslationRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicTranslations, TranslationV1{
		OpportunityID: "opp_1", Lang: "sw", TitleTr: "Mhandisi",
		DescriptionTr: "Tunaajiri...", ModelVersion: "v1",
		TranslatedAt: time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC),
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
		OpportunityID: "opp_1", Slug: "job-slug", Kind: "job", R2Version: 3,
		PublishedAt: time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC),
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

func TestCVUploadedRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicCVUploaded, CVUploadedV1{
		CandidateID:   "cnd_1",
		CVVersion:     1,
		RawArchiveRef: "raw/abc123",
		Filename:      "resume.pdf",
		ContentType:   "application/pdf",
		SizeBytes:     12345,
	})
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Envelope[CVUploadedV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.CandidateID != "cnd_1" || back.Payload.CVVersion != 1 {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestCVExtractedRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicCVExtracted, CVExtractedV1{
		CandidateID:         "cnd_1",
		CVVersion:           1,
		Name:                "Jane Doe",
		Email:               "jane@example.com",
		Seniority:           "senior",
		YearsExperience:     8,
		PrimaryIndustry:     "technology",
		StrongSkills:        []string{"Go", "Kubernetes"},
		WorkingSkills:       []string{"Python"},
		ScoreOverall:        82,
		ScoreATS:            85,
		ScoreKeywords:       78,
		ScoreImpact:         80,
		ScoreRoleFit:        84,
		ScoreClarity:        83,
		ModelVersionExtract: "ext-v1",
		ModelVersionScore:   "score-v1",
	})
	raw, _ := json.Marshal(orig)
	var back Envelope[CVExtractedV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.CandidateID != "cnd_1" || back.Payload.ScoreOverall != 82 {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestCVImprovedRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicCVImproved, CVImprovedV1{
		CandidateID: "cnd_1",
		CVVersion:   1,
		Fixes: []CVFix{{
			FixID:          "fix-1",
			Title:          "Add quantified impact",
			ImpactLevel:    "high",
			Category:       "impact",
			Why:            "Bullets lack numbers",
			AutoApplicable: true,
			Rewrite:        "Reduced latency 40%",
		}},
		ModelVersion: "improve-v1",
	})
	raw, _ := json.Marshal(orig)
	var back Envelope[CVImprovedV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(back.Payload.Fixes) != 1 || back.Payload.Fixes[0].FixID != "fix-1" {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestCandidateEmbeddingRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicCandidateEmbedding, CandidateEmbeddingV1{
		CandidateID:  "cnd_1",
		CVVersion:    1,
		Vector:       []float32{0.1, 0.2, 0.3},
		ModelVersion: "embed-v1",
	})
	raw, _ := json.Marshal(orig)
	var back Envelope[CandidateEmbeddingV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.CandidateID != "cnd_1" || len(back.Payload.Vector) != 3 {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestCandidatePreferencesUpdatedRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicCandidatePreferencesUpdated, PreferencesUpdatedV1{
		CandidateID:        "cnd_1",
		RemotePreference:   "remote",
		SalaryMin:          80000,
		SalaryMax:          140000,
		Currency:           "USD",
		PreferredLocations: []string{"KE", "US"},
		ExcludedCompanies:  []string{"BadCo"},
		TargetRoles:        []string{"backend-engineer"},
		Languages:          []string{"en"},
		Availability:       "2-weeks",
	})
	raw, _ := json.Marshal(orig)
	var back Envelope[PreferencesUpdatedV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.SalaryMin != 80000 || len(back.Payload.PreferredLocations) != 2 {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestMatchesReadyRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicCandidateMatchesReady, MatchesReadyV1{
		CandidateID:  "cnd_1",
		MatchBatchID: "batch_1",
		Matches: []MatchRow{
			{CanonicalID: "can_a", Score: 0.91, RerankScore: 0.94},
			{CanonicalID: "can_b", Score: 0.83},
		},
	})
	raw, _ := json.Marshal(orig)
	var back Envelope[MatchesReadyV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(back.Payload.Matches) != 2 || back.Payload.Matches[0].CanonicalID != "can_a" {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestPartitionKeyCVEventsUseCandidatePrefix(t *testing.T) {
	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	for _, topic := range []string{
		TopicCVUploaded, TopicCVExtracted, TopicCVImproved,
		TopicCandidateEmbedding, TopicCandidatePreferencesUpdated,
		TopicCandidateMatchesReady,
	} {
		pk := PartitionKey(topic, now, "abcdef123456")
		if pk.Secondary != "ab" {
			t.Fatalf("topic=%s Secondary=%q, want ab", topic, pk.Secondary)
		}
	}
}

func TestPartitionObjectPathCandidateCVLabel(t *testing.T) {
	pk := PartKey{DT: "2026-04-21", Secondary: "ab"}
	got := pk.ObjectPath("candidates_cv", "xyz789")
	want := "candidates_cv/dt=2026-04-21/cnd=ab/xyz789.parquet"
	if got != want {
		t.Fatalf("path=%q, want %q", got, want)
	}
}

func TestPartitionObjectPathCandidatesPreferencesLabel(t *testing.T) {
	pk := PartKey{DT: "2026-04-21", Secondary: "cd"}
	got := pk.ObjectPath("candidates_preferences", "xyz789")
	want := "candidates_preferences/dt=2026-04-21/cnd=cd/xyz789.parquet"
	if got != want {
		t.Fatalf("path=%q, want %q", got, want)
	}
}
