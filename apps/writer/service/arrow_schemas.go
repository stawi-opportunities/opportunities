package service

// arrow_schemas.go — Arrow schema definitions for all 18 event types
// written to Iceberg. Field order, names, and types MUST match the
// pyiceberg schema in definitions/iceberg/_schemas.py. Nullable
// corresponds to optional() in Iceberg.
//
// Timestamp columns use Timestamp_us (microsecond) to match Iceberg's
// TimestamptzType. String lists use ListOf(String). Float lists use
// ListOf(Float32). Nested structs use StructOf(...).

import (
	"github.com/apache/arrow-go/v18/arrow"
)

// --------------------------------------------------------------------
// Shared field helpers
// --------------------------------------------------------------------

func _req(name string, dt arrow.DataType) arrow.Field {
	return arrow.Field{Name: name, Type: dt, Nullable: false}
}

func _opt(name string, dt arrow.DataType) arrow.Field {
	return arrow.Field{Name: name, Type: dt, Nullable: true}
}

// _strListType is the Arrow representation of []string columns:
// a non-nullable list of non-nullable strings.
var _strListType = arrow.ListOf(arrow.BinaryTypes.String)

// _floatListType is the Arrow representation of []float32 vector
// columns.
var _floatListType = arrow.ListOf(arrow.PrimitiveTypes.Float32)

// _tsType is the timestamp type for all occurred_at / posted_at fields —
// matches Iceberg TimestamptzType (µs precision).
var _tsType arrow.DataType = &arrow.TimestampType{Unit: arrow.Microsecond, TimeZone: "UTC"}

// --------------------------------------------------------------------
// jobs.variants  (VariantIngestedV1 + all variant pipeline stages)
// --------------------------------------------------------------------

var ArrowSchemaVariants = arrow.NewSchema([]arrow.Field{
	_req("variant_id", arrow.BinaryTypes.String),
	_req("source_id", arrow.BinaryTypes.String),
	_req("external_id", arrow.BinaryTypes.String),
	_req("hard_key", arrow.BinaryTypes.String),
	_req("stage", arrow.BinaryTypes.String),
	_opt("title", arrow.BinaryTypes.String),
	_opt("company", arrow.BinaryTypes.String),
	_opt("location_text", arrow.BinaryTypes.String),
	_opt("country", arrow.BinaryTypes.String),
	_opt("language", arrow.BinaryTypes.String),
	_opt("remote_type", arrow.BinaryTypes.String),
	_opt("employment_type", arrow.BinaryTypes.String),
	_opt("salary_min", arrow.PrimitiveTypes.Float64),
	_opt("salary_max", arrow.PrimitiveTypes.Float64),
	_opt("currency", arrow.BinaryTypes.String),
	_opt("description", arrow.BinaryTypes.String),
	_opt("apply_url", arrow.BinaryTypes.String),
	_opt("posted_at", _tsType),
	_opt("scraped_at", _tsType),
	_opt("content_hash", arrow.BinaryTypes.String),
	_opt("raw_archive_ref", arrow.BinaryTypes.String),
	_opt("model_version_extract", arrow.BinaryTypes.String),
	// envelope tail
	_req("event_id", arrow.BinaryTypes.String),
	_req("occurred_at", _tsType),
}, nil)

// jobs.canonicals and jobs.canonicals_expired are NOT persisted to Iceberg.
// Canonical body lives at s3://opportunities-content/jobs/<slug>.json (R2-direct).
// canonicals_expired is a Frame event only — the materializer subscribes directly.

// --------------------------------------------------------------------
// jobs.embeddings  (EmbeddingV1)
// --------------------------------------------------------------------

var ArrowSchemaEmbeddings = arrow.NewSchema([]arrow.Field{
	_req("canonical_id", arrow.BinaryTypes.String),
	_req("vector", _floatListType),
	_req("model_version", arrow.BinaryTypes.String),
	// envelope tail
	_req("event_id", arrow.BinaryTypes.String),
	_req("occurred_at", _tsType),
}, nil)

// jobs.translations is NOT persisted to Iceberg.
// Translated body lives at s3://opportunities-content/jobs/<slug>/<lang>.json (R2-direct).

// --------------------------------------------------------------------
// jobs.published  (PublishedV1)
// --------------------------------------------------------------------

var ArrowSchemaPublished = arrow.NewSchema([]arrow.Field{
	_req("canonical_id", arrow.BinaryTypes.String),
	_req("slug", arrow.BinaryTypes.String),
	_req("r2_version", arrow.PrimitiveTypes.Int32),
	_req("published_at", _tsType),
	// envelope tail
	_req("event_id", arrow.BinaryTypes.String),
	_req("occurred_at", _tsType),
}, nil)

// --------------------------------------------------------------------
// jobs.crawl_page_completed  (CrawlPageCompletedV1)
// --------------------------------------------------------------------

var ArrowSchemaCrawlPageCompleted = arrow.NewSchema([]arrow.Field{
	_req("request_id", arrow.BinaryTypes.String),
	_req("source_id", arrow.BinaryTypes.String),
	_opt("url", arrow.BinaryTypes.String),
	_opt("http_status", arrow.PrimitiveTypes.Int32),
	_req("jobs_found", arrow.PrimitiveTypes.Int32),
	_req("jobs_emitted", arrow.PrimitiveTypes.Int32),
	_req("jobs_rejected", arrow.PrimitiveTypes.Int32),
	_opt("cursor", arrow.BinaryTypes.String),
	_opt("error_code", arrow.BinaryTypes.String),
	_opt("error_message", arrow.BinaryTypes.String),
	// envelope tail
	_req("event_id", arrow.BinaryTypes.String),
	_req("occurred_at", _tsType),
}, nil)

// --------------------------------------------------------------------
// jobs.sources_discovered  (SourceDiscoveredV1)
// --------------------------------------------------------------------

var ArrowSchemaSourcesDiscovered = arrow.NewSchema([]arrow.Field{
	_req("source_id", arrow.BinaryTypes.String),
	_req("discovered_url", arrow.BinaryTypes.String),
	_opt("name", arrow.BinaryTypes.String),
	_opt("country", arrow.BinaryTypes.String),
	_opt("type", arrow.BinaryTypes.String),
	// envelope tail
	_req("event_id", arrow.BinaryTypes.String),
	_req("occurred_at", _tsType),
}, nil)

// --------------------------------------------------------------------
// candidates.cv_uploaded  (CVUploadedV1)
// --------------------------------------------------------------------

var ArrowSchemaCVUploaded = arrow.NewSchema([]arrow.Field{
	_req("candidate_id", arrow.BinaryTypes.String),
	_req("cv_version", arrow.PrimitiveTypes.Int32),
	_req("raw_archive_ref", arrow.BinaryTypes.String),
	_opt("filename", arrow.BinaryTypes.String),
	_opt("content_type", arrow.BinaryTypes.String),
	_opt("size_bytes", arrow.PrimitiveTypes.Int64),
	_req("extracted_text", arrow.BinaryTypes.String),
	// envelope tail
	_req("event_id", arrow.BinaryTypes.String),
	_req("occurred_at", _tsType),
}, nil)

// --------------------------------------------------------------------
// candidates.cv_extracted  (CVExtractedV1)
// --------------------------------------------------------------------

var ArrowSchemaCVExtracted = arrow.NewSchema([]arrow.Field{
	_req("candidate_id", arrow.BinaryTypes.String),
	_req("cv_version", arrow.PrimitiveTypes.Int32),
	_opt("name", arrow.BinaryTypes.String),
	_opt("email", arrow.BinaryTypes.String),
	_opt("phone", arrow.BinaryTypes.String),
	_opt("location", arrow.BinaryTypes.String),
	_opt("current_title", arrow.BinaryTypes.String),
	_opt("bio", arrow.BinaryTypes.String),
	_opt("seniority", arrow.BinaryTypes.String),
	_opt("years_experience", arrow.PrimitiveTypes.Int32),
	_opt("primary_industry", arrow.BinaryTypes.String),
	_opt("strong_skills", _strListType),
	_opt("working_skills", _strListType),
	_opt("tools_frameworks", _strListType),
	_opt("certifications", _strListType),
	_opt("preferred_roles", _strListType),
	_opt("languages", _strListType),
	_opt("education", arrow.BinaryTypes.String),
	_opt("preferred_locations", _strListType),
	_opt("remote_preference", arrow.BinaryTypes.String),
	_opt("salary_min", arrow.PrimitiveTypes.Int32),
	_opt("salary_max", arrow.PrimitiveTypes.Int32),
	_opt("currency", arrow.BinaryTypes.String),
	_req("score_ats", arrow.PrimitiveTypes.Int32),
	_req("score_keywords", arrow.PrimitiveTypes.Int32),
	_req("score_impact", arrow.PrimitiveTypes.Int32),
	_req("score_role_fit", arrow.PrimitiveTypes.Int32),
	_req("score_clarity", arrow.PrimitiveTypes.Int32),
	_req("score_overall", arrow.PrimitiveTypes.Int32),
	_opt("model_version_extract", arrow.BinaryTypes.String),
	_opt("model_version_score", arrow.BinaryTypes.String),
	// envelope tail
	_req("event_id", arrow.BinaryTypes.String),
	_req("occurred_at", _tsType),
}, nil)

// --------------------------------------------------------------------
// candidates.cv_improved  (CVImprovedV1)
// CVFix struct: fix_id (req), title (opt), impact (opt), category (opt),
//               why (opt), auto_applicable (req bool), rewrite (opt).
// --------------------------------------------------------------------

// _cvFixType is the Arrow struct type for the CVFix element.
var _cvFixType = arrow.StructOf(
	_req("fix_id", arrow.BinaryTypes.String),
	_opt("title", arrow.BinaryTypes.String),
	_opt("impact", arrow.BinaryTypes.String),
	_opt("category", arrow.BinaryTypes.String),
	_opt("why", arrow.BinaryTypes.String),
	_req("auto_applicable", arrow.FixedWidthTypes.Boolean),
	_opt("rewrite", arrow.BinaryTypes.String),
)

// _cvFixListType is List<CVFix> (nullable list elements — matches
// element_required=False in the pyiceberg schema).
var _cvFixListType = arrow.ListOfField(arrow.Field{
	Name:     "item",
	Type:     _cvFixType,
	Nullable: true,
})

var ArrowSchemaCVImproved = arrow.NewSchema([]arrow.Field{
	_req("candidate_id", arrow.BinaryTypes.String),
	_req("cv_version", arrow.PrimitiveTypes.Int32),
	_req("fixes", _cvFixListType),
	_opt("model_version", arrow.BinaryTypes.String),
	// envelope tail
	_req("event_id", arrow.BinaryTypes.String),
	_req("occurred_at", _tsType),
}, nil)

// --------------------------------------------------------------------
// candidates.embeddings  (CandidateEmbeddingV1)
// --------------------------------------------------------------------

var ArrowSchemaCandidateEmbeddings = arrow.NewSchema([]arrow.Field{
	_req("candidate_id", arrow.BinaryTypes.String),
	_req("cv_version", arrow.PrimitiveTypes.Int32),
	_req("vector", _floatListType),
	_opt("model_version", arrow.BinaryTypes.String),
	// envelope tail
	_req("event_id", arrow.BinaryTypes.String),
	_req("occurred_at", _tsType),
}, nil)

// --------------------------------------------------------------------
// candidates.preferences  (PreferencesUpdatedV1)
// --------------------------------------------------------------------

var ArrowSchemaPreferences = arrow.NewSchema([]arrow.Field{
	_req("candidate_id", arrow.BinaryTypes.String),
	_opt("remote_preference", arrow.BinaryTypes.String),
	_opt("salary_min", arrow.PrimitiveTypes.Int32),
	_opt("salary_max", arrow.PrimitiveTypes.Int32),
	_opt("currency", arrow.BinaryTypes.String),
	_opt("preferred_locations", _strListType),
	_opt("excluded_companies", _strListType),
	_opt("target_roles", _strListType),
	_opt("languages", _strListType),
	_opt("availability", arrow.BinaryTypes.String),
	// envelope tail
	_req("event_id", arrow.BinaryTypes.String),
	_req("occurred_at", _tsType),
}, nil)

// --------------------------------------------------------------------
// candidates.matches_ready  (MatchesReadyV1)
// MatchRow struct: canonical_id (req), score (req f64), rerank_score (opt f64).
// --------------------------------------------------------------------

// _matchRowType is the Arrow struct type for the MatchRow element.
var _matchRowType = arrow.StructOf(
	_req("canonical_id", arrow.BinaryTypes.String),
	_req("score", arrow.PrimitiveTypes.Float64),
	_opt("rerank_score", arrow.PrimitiveTypes.Float64),
)

// _matchRowListType is List<MatchRow> (nullable list elements).
var _matchRowListType = arrow.ListOfField(arrow.Field{
	Name:     "item",
	Type:     _matchRowType,
	Nullable: true,
})

var ArrowSchemaMatchesReady = arrow.NewSchema([]arrow.Field{
	_req("candidate_id", arrow.BinaryTypes.String),
	_req("match_batch_id", arrow.BinaryTypes.String),
	_req("matches", _matchRowListType),
	// envelope tail
	_req("event_id", arrow.BinaryTypes.String),
	_req("occurred_at", _tsType),
}, nil)
