package service_test

// arrow_build_test.go — unit tests for all 18 BuildXxxRecord builders.
//
// Each test:
//  1. Constructs a representative envelope JSON.
//  2. Calls the builder with a fresh GoAllocator.
//  3. Asserts the RecordReader has exactly one row.
//  4. Spot-checks key field values.
//
// No containers required — all pure in-process.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	eventsv1 "stawi.jobs/pkg/events/v1"
	writersvc "stawi.jobs/apps/writer/service"
)

var testPool = memory.NewGoAllocator()

// readOneRow advances a RecordReader expecting exactly 1 row in 1 batch
// and returns the single RecordBatch (caller is responsible for release).
func readOneRow(t *testing.T, rdr array.RecordReader) (arrow.RecordBatch, func()) {
	t.Helper()
	require.True(t, rdr.Next(), "expected at least one batch")
	rec := rdr.RecordBatch()
	rec.Retain()
	require.EqualValues(t, 1, rec.NumRows(), "expected exactly 1 row")
	return rec, func() { rec.Release() }
}

func marshalEnv[T any](t *testing.T, topic string, payload T) json.RawMessage {
	t.Helper()
	env := eventsv1.NewEnvelope(topic, payload)
	raw, err := json.Marshal(env)
	require.NoError(t, err)
	return raw
}

// -----------------------------------------------------------------------
// jobs.variants family
// -----------------------------------------------------------------------

func TestBuildVariantIngestedRecord(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	raw := marshalEnv(t, eventsv1.TopicVariantsIngested, eventsv1.VariantIngestedV1{
		VariantID:  "var-1",
		SourceID:   "acme",
		ExternalID: "ext-1",
		HardKey:    "acme|ext-1",
		Stage:      "ingested",
		Title:      "Go Engineer",
		ScrapedAt:  now,
	})

	rdr, err := writersvc.BuildVariantIngestedRecord(testPool, []json.RawMessage{raw})
	require.NoError(t, err)
	defer rdr.Release()

	rec, cleanup := readOneRow(t, rdr)
	defer cleanup()

	assert.Equal(t, "var-1", rec.Column(0).(*array.String).Value(0))
	assert.Equal(t, "acme", rec.Column(1).(*array.String).Value(0))
	assert.Equal(t, "ingested", rec.Column(4).(*array.String).Value(0))
	assert.Equal(t, "Go Engineer", rec.Column(5).(*array.String).Value(0))
	// scraped_at (field 18) — microsecond timestamp
	assert.False(t, rec.Column(18).IsNull(0))
}

func TestBuildVariantNormalizedRecord(t *testing.T) {
	raw := marshalEnv(t, eventsv1.TopicVariantsNormalized, eventsv1.VariantNormalizedV1{
		VariantID: "var-2",
		SourceID:  "beta",
		HardKey:   "beta|v2",
		Stage:     "normalized",
		ScrapedAt: time.Now().UTC(),
	})
	rdr, err := writersvc.BuildVariantNormalizedRecord(testPool, []json.RawMessage{raw})
	require.NoError(t, err)
	defer rdr.Release()
	rec, cleanup := readOneRow(t, rdr)
	defer cleanup()
	assert.Equal(t, "var-2", rec.Column(0).(*array.String).Value(0))
	assert.Equal(t, "normalized", rec.Column(4).(*array.String).Value(0))
}

func TestBuildVariantValidatedRecord(t *testing.T) {
	raw := marshalEnv(t, eventsv1.TopicVariantsValidated, eventsv1.VariantValidatedV1{
		VariantID:       "var-3",
		ValidationScore: 0.9,
		Normalized: eventsv1.VariantNormalizedV1{
			VariantID: "var-3",
			SourceID:  "gamma",
			HardKey:   "gamma|v3",
			Stage:     "normalized",
			ScrapedAt: time.Now().UTC(),
		},
	})
	rdr, err := writersvc.BuildVariantValidatedRecord(testPool, []json.RawMessage{raw})
	require.NoError(t, err)
	defer rdr.Release()
	rec, cleanup := readOneRow(t, rdr)
	defer cleanup()
	assert.Equal(t, "var-3", rec.Column(0).(*array.String).Value(0))
	assert.Equal(t, "validated", rec.Column(4).(*array.String).Value(0))
}

func TestBuildVariantFlaggedRecord(t *testing.T) {
	raw := marshalEnv(t, eventsv1.TopicVariantsFlagged, eventsv1.VariantFlaggedV1{
		VariantID: "var-4",
		SourceID:  "delta",
		Reason:    "low_quality",
	})
	rdr, err := writersvc.BuildVariantFlaggedRecord(testPool, []json.RawMessage{raw})
	require.NoError(t, err)
	defer rdr.Release()
	rec, cleanup := readOneRow(t, rdr)
	defer cleanup()
	assert.Equal(t, "var-4", rec.Column(0).(*array.String).Value(0))
	assert.Equal(t, "flagged", rec.Column(4).(*array.String).Value(0))
}

func TestBuildVariantClusteredRecord(t *testing.T) {
	raw := marshalEnv(t, eventsv1.TopicVariantsClustered, eventsv1.VariantClusteredV1{
		VariantID: "var-5",
		ClusterID: "clust-1",
		Validated: eventsv1.VariantValidatedV1{
			VariantID: "var-5",
			Normalized: eventsv1.VariantNormalizedV1{
				VariantID: "var-5",
				SourceID:  "epsilon",
				HardKey:   "epsilon|v5",
				Stage:     "normalized",
				ScrapedAt: time.Now().UTC(),
			},
		},
	})
	rdr, err := writersvc.BuildVariantClusteredRecord(testPool, []json.RawMessage{raw})
	require.NoError(t, err)
	defer rdr.Release()
	rec, cleanup := readOneRow(t, rdr)
	defer cleanup()
	assert.Equal(t, "var-5", rec.Column(0).(*array.String).Value(0))
	assert.Equal(t, "clustered", rec.Column(4).(*array.String).Value(0))
}

// jobs.canonicals and jobs.canonicals_expired Arrow builders removed —
// these topics are no longer persisted to Iceberg (R2-slug-direct and
// Frame-subscriber paths respectively).

// -----------------------------------------------------------------------
// jobs.embeddings
// -----------------------------------------------------------------------

func TestBuildEmbeddingRecord(t *testing.T) {
	raw := marshalEnv(t, eventsv1.TopicEmbeddings, eventsv1.EmbeddingV1{
		CanonicalID:  "can-3",
		Vector:       []float32{0.1, 0.2, 0.3},
		ModelVersion: "text-embed-v1",
	})
	rdr, err := writersvc.BuildEmbeddingRecord(testPool, []json.RawMessage{raw})
	require.NoError(t, err)
	defer rdr.Release()
	rec, cleanup := readOneRow(t, rdr)
	defer cleanup()
	assert.Equal(t, "can-3", rec.Column(0).(*array.String).Value(0))
	// vector field (1) is a list; check length
	vecList := rec.Column(1).(*array.List)
	start, end := vecList.ValueOffsets(0)
	assert.EqualValues(t, 3, end-start)
}

// jobs.translations Arrow builder removed — translations are no longer
// persisted to Iceberg (R2-slug-direct: jobs/<slug>/<lang>.json).

// -----------------------------------------------------------------------
// jobs.published
// -----------------------------------------------------------------------

func TestBuildPublishedRecord(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	raw := marshalEnv(t, eventsv1.TopicPublished, eventsv1.PublishedV1{
		CanonicalID: "can-5",
		Slug:        "go-engineer-acme",
		R2Version:   3,
		PublishedAt: now,
	})
	rdr, err := writersvc.BuildPublishedRecord(testPool, []json.RawMessage{raw})
	require.NoError(t, err)
	defer rdr.Release()
	rec, cleanup := readOneRow(t, rdr)
	defer cleanup()
	assert.Equal(t, "can-5", rec.Column(0).(*array.String).Value(0))
	assert.EqualValues(t, 3, rec.Column(2).(*array.Int32).Value(0))
}

// -----------------------------------------------------------------------
// jobs.crawl_page_completed
// -----------------------------------------------------------------------

func TestBuildCrawlPageCompletedRecord(t *testing.T) {
	raw := marshalEnv(t, eventsv1.TopicCrawlPageCompleted, eventsv1.CrawlPageCompletedV1{
		RequestID:    "req-1",
		SourceID:     "acme",
		JobsFound:    10,
		JobsEmitted:  8,
		JobsRejected: 2,
	})
	rdr, err := writersvc.BuildCrawlPageCompletedRecord(testPool, []json.RawMessage{raw})
	require.NoError(t, err)
	defer rdr.Release()
	rec, cleanup := readOneRow(t, rdr)
	defer cleanup()
	assert.Equal(t, "req-1", rec.Column(0).(*array.String).Value(0))
	assert.EqualValues(t, 10, rec.Column(4).(*array.Int32).Value(0))
	assert.EqualValues(t, 8, rec.Column(5).(*array.Int32).Value(0))
}

// -----------------------------------------------------------------------
// jobs.sources_discovered
// -----------------------------------------------------------------------

func TestBuildSourceDiscoveredRecord(t *testing.T) {
	raw := marshalEnv(t, eventsv1.TopicSourcesDiscovered, eventsv1.SourceDiscoveredV1{
		SourceID:      "src-1",
		DiscoveredURL: "https://jobs.example.com",
		Country:       "KE",
	})
	rdr, err := writersvc.BuildSourceDiscoveredRecord(testPool, []json.RawMessage{raw})
	require.NoError(t, err)
	defer rdr.Release()
	rec, cleanup := readOneRow(t, rdr)
	defer cleanup()
	assert.Equal(t, "src-1", rec.Column(0).(*array.String).Value(0))
	assert.Equal(t, "https://jobs.example.com", rec.Column(1).(*array.String).Value(0))
}

// -----------------------------------------------------------------------
// candidates
// -----------------------------------------------------------------------

func TestBuildCVUploadedRecord(t *testing.T) {
	raw := marshalEnv(t, eventsv1.TopicCVUploaded, eventsv1.CVUploadedV1{
		CandidateID:   "cand-1",
		CVVersion:     1,
		RawArchiveRef: "raw/abc",
		ExtractedText: "John Doe...",
		SizeBytes:     12345,
	})
	rdr, err := writersvc.BuildCVUploadedRecord(testPool, []json.RawMessage{raw})
	require.NoError(t, err)
	defer rdr.Release()
	rec, cleanup := readOneRow(t, rdr)
	defer cleanup()
	assert.Equal(t, "cand-1", rec.Column(0).(*array.String).Value(0))
	assert.EqualValues(t, 1, rec.Column(1).(*array.Int32).Value(0))
	assert.EqualValues(t, 12345, rec.Column(5).(*array.Int64).Value(0))
}

func TestBuildCVExtractedRecord(t *testing.T) {
	raw := marshalEnv(t, eventsv1.TopicCVExtracted, eventsv1.CVExtractedV1{
		CandidateID:  "cand-2",
		CVVersion:    2,
		Name:         "Alice",
		StrongSkills: []string{"Go", "Python"},
		ScoreATS:     85,
		ScoreOverall: 78,
	})
	rdr, err := writersvc.BuildCVExtractedRecord(testPool, []json.RawMessage{raw})
	require.NoError(t, err)
	defer rdr.Release()
	rec, cleanup := readOneRow(t, rdr)
	defer cleanup()
	assert.Equal(t, "cand-2", rec.Column(0).(*array.String).Value(0))
	// strong_skills is field 11 (list)
	skillList := rec.Column(11).(*array.List)
	s, e := skillList.ValueOffsets(0)
	assert.EqualValues(t, 2, e-s)
	assert.EqualValues(t, 85, rec.Column(23).(*array.Int32).Value(0))
}

func TestBuildCVImprovedRecord(t *testing.T) {
	raw := marshalEnv(t, eventsv1.TopicCVImproved, eventsv1.CVImprovedV1{
		CandidateID: "cand-3",
		CVVersion:   1,
		Fixes: []eventsv1.CVFix{
			{FixID: "fix-1", Title: "Add impact verbs", AutoApplicable: true},
			{FixID: "fix-2", ImpactLevel: "high", AutoApplicable: false},
		},
		ModelVersion: "cv-improve-v2",
	})
	rdr, err := writersvc.BuildCVImprovedRecord(testPool, []json.RawMessage{raw})
	require.NoError(t, err)
	defer rdr.Release()
	rec, cleanup := readOneRow(t, rdr)
	defer cleanup()
	assert.Equal(t, "cand-3", rec.Column(0).(*array.String).Value(0))
	// fixes is field 2 (list of structs)
	fixList := rec.Column(2).(*array.List)
	fs, fe := fixList.ValueOffsets(0)
	assert.EqualValues(t, 2, fe-fs)
}

func TestBuildCandidateEmbeddingRecord(t *testing.T) {
	raw := marshalEnv(t, eventsv1.TopicCandidateEmbedding, eventsv1.CandidateEmbeddingV1{
		CandidateID:  "cand-4",
		CVVersion:    1,
		Vector:       []float32{0.5, 0.6, 0.7, 0.8},
		ModelVersion: "cv-embed-v1",
	})
	rdr, err := writersvc.BuildCandidateEmbeddingRecord(testPool, []json.RawMessage{raw})
	require.NoError(t, err)
	defer rdr.Release()
	rec, cleanup := readOneRow(t, rdr)
	defer cleanup()
	assert.Equal(t, "cand-4", rec.Column(0).(*array.String).Value(0))
	vecList := rec.Column(2).(*array.List)
	vs, ve := vecList.ValueOffsets(0)
	assert.EqualValues(t, 4, ve-vs)
}

func TestBuildPreferencesRecord(t *testing.T) {
	raw := marshalEnv(t, eventsv1.TopicCandidatePreferencesUpdated, eventsv1.PreferencesUpdatedV1{
		CandidateID:        "cand-5",
		RemotePreference:   "remote_only",
		SalaryMin:          80000,
		SalaryMax:          120000,
		Currency:           "USD",
		PreferredLocations: []string{"Nairobi", "Lagos"},
		TargetRoles:        []string{"Backend Engineer"},
	})
	rdr, err := writersvc.BuildPreferencesRecord(testPool, []json.RawMessage{raw})
	require.NoError(t, err)
	defer rdr.Release()
	rec, cleanup := readOneRow(t, rdr)
	defer cleanup()
	assert.Equal(t, "cand-5", rec.Column(0).(*array.String).Value(0))
	assert.Equal(t, "remote_only", rec.Column(1).(*array.String).Value(0))
	locList := rec.Column(5).(*array.List)
	ls, le := locList.ValueOffsets(0)
	assert.EqualValues(t, 2, le-ls)
}

func TestBuildMatchesReadyRecord(t *testing.T) {
	raw := marshalEnv(t, eventsv1.TopicCandidateMatchesReady, eventsv1.MatchesReadyV1{
		CandidateID:  "cand-6",
		MatchBatchID: "batch-1",
		Matches: []eventsv1.MatchRow{
			{CanonicalID: "can-10", Score: 0.92},
			{CanonicalID: "can-11", Score: 0.85, RerankScore: 0.88},
		},
	})
	rdr, err := writersvc.BuildMatchesReadyRecord(testPool, []json.RawMessage{raw})
	require.NoError(t, err)
	defer rdr.Release()
	rec, cleanup := readOneRow(t, rdr)
	defer cleanup()
	assert.Equal(t, "cand-6", rec.Column(0).(*array.String).Value(0))
	assert.Equal(t, "batch-1", rec.Column(1).(*array.String).Value(0))
	matchList := rec.Column(2).(*array.List)
	ms, me := matchList.ValueOffsets(0)
	assert.EqualValues(t, 2, me-ms)
}
