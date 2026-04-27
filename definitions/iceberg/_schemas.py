"""
DEPRECATED — kept as a documentation artifact.

The canonical, source-of-truth Iceberg schemas now live in Go at
pkg/icebergclient/schemas.go and are materialised at deploy time by the
`bootstrap-iceberg` subcommand of the writer image. Edits here have no
effect on the deployed catalog.

Shared Iceberg schema definitions for all 12 stawi-opportunities Iceberg tables.

Removed from Iceberg (body lives in R2 slug-direct JSON):
  - CANONICALS          → s3://product-opportunities-content/jobs/<slug>.json
  - CANONICALS_EXPIRED  → Frame event only; materializer subscribes directly
  - TRANSLATIONS        → s3://product-opportunities-content/jobs/<slug>/<lang>.json

Phase 3.2 (opportunity-generification) reshaped opportunities.variants /
opportunities.published / opportunities.embeddings to the polymorphic
Opportunity shape and added opportunities.variants_rejected as a sink
for Verify-stage rejections. The polymorphic tables drop the legacy
event_id / occurred_at envelope tail in favour of a dedicated
scraped_at / rejected_at / embedded_at / published_at column — the
event timestamp on these tables IS the business timestamp, so we don't
double-store it.

Field IDs start at 1 per table and are assigned in declaration order.
Optional fields from Go structs (omitempty) map to optional() in
Iceberg.
"""

from pyiceberg.schema import Schema
from pyiceberg.types import (
    BooleanType,
    DoubleType,
    FloatType,
    IntegerType,
    ListType,
    LongType,
    NestedField,
    StringType,
    StructType,
    TimestampType,
    TimestamptzType,
)

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _req(field_id: int, name: str, field_type) -> NestedField:
    return NestedField(field_id=field_id, name=name, field_type=field_type, required=True)

def _opt(field_id: int, name: str, field_type) -> NestedField:
    return NestedField(field_id=field_id, name=name, field_type=field_type, required=False)

def _str_list(element_id: int) -> ListType:
    """ListType for []string — element is required (non-nullable strings)."""
    return ListType(element_id=element_id, element_type=StringType(), element_required=True)

def _float_list(element_id: int) -> ListType:
    """ListType for []float32 vector columns."""
    return ListType(element_id=element_id, element_type=FloatType(), element_required=True)

# ---------------------------------------------------------------------------
# Envelope tail — appended to every schema (last two fields).
# Call _envelope(next_id) → (field_event_id, field_occurred_at)
# ---------------------------------------------------------------------------

def _envelope(base_id: int):
    return [
        _req(base_id,     "event_id",    StringType()),
        _req(base_id + 1, "occurred_at", TimestamptzType()),
    ]

# ---------------------------------------------------------------------------
# opportunities namespace
# ---------------------------------------------------------------------------

# opportunities.variants  (VariantIngestedV1 — all variant pipeline stages
# write here, distinguished by the "stage" column).
#
# Polymorphic shape: universal columns up front, kind discriminator + JSON
# attributes blob for kind-specific extension. categories is a comma-joined
# string (cheap analytics filter) rather than a list — full taxonomy lives
# in attributes JSON when needed.
VARIANTS = Schema(
    NestedField(1,  "variant_id",     StringType(),    required=True),
    NestedField(2,  "source_id",      StringType(),    required=True),
    NestedField(3,  "external_id",    StringType(),    required=True),
    NestedField(4,  "hard_key",       StringType(),    required=True),
    NestedField(5,  "kind",           StringType(),    required=True),
    NestedField(6,  "stage",          StringType(),    required=True),
    NestedField(7,  "title",          StringType(),    required=True),
    NestedField(8,  "issuing_entity", StringType(),    required=False),
    NestedField(9,  "country",        StringType(),    required=False),
    NestedField(10, "region",         StringType(),    required=False),
    NestedField(11, "city",           StringType(),    required=False),
    NestedField(12, "lat",            DoubleType(),    required=False),
    NestedField(13, "lon",            DoubleType(),    required=False),
    NestedField(14, "remote",         BooleanType(),   required=False),
    NestedField(15, "geo_scope",      StringType(),    required=False),
    NestedField(16, "currency",       StringType(),    required=False),
    NestedField(17, "amount_min",     DoubleType(),    required=False),
    NestedField(18, "amount_max",     DoubleType(),    required=False),
    NestedField(19, "deadline",       TimestampType(), required=False),
    NestedField(20, "categories",     StringType(),    required=False),  # comma-joined
    NestedField(21, "attributes",     StringType(),    required=False),  # JSON
    NestedField(22, "scraped_at",     TimestampType(), required=True),
)

# opportunities.variants_rejected — Verify-stage sink for variants that
# fail Spec-required-attribute checks. Audit trail; never republished.
VARIANTS_REJECTED = Schema(
    NestedField(1, "variant_id",  StringType(),    required=True),
    NestedField(2, "source_id",   StringType(),    required=True),
    NestedField(3, "kind",        StringType(),    required=True),
    NestedField(4, "title",       StringType(),    required=True),
    NestedField(5, "reasons",     StringType(),    required=True),  # JSON array
    NestedField(6, "rejected_at", TimestampType(), required=True),
)

# opportunities.canonicals and opportunities.canonicals_expired schemas removed from Iceberg.
# Canonical body → s3://opportunities-content/opportunities/<slug>.json (R2-slug-direct).
# canonicals_expired → Frame event only; materializer subscribes directly.

# opportunities.embeddings  (EmbeddingV1)
# vector is List<DoubleType()> — analytics-friendly width-agnostic.
EMBEDDINGS = Schema(
    NestedField(1, "variant_id",  StringType(),    required=True),
    NestedField(2, "kind",        StringType(),    required=True),
    NestedField(3, "vector",
                ListType(element_id=100, element_type=DoubleType(), element_required=True),
                required=True),
    NestedField(4, "embedded_at", TimestampType(), required=True),
)

# opportunities.translations schema removed from Iceberg.
# Translated body → s3://opportunities-content/opportunities/<slug>/<lang>.json (R2-slug-direct).

# opportunities.published — mirrors VARIANTS column-for-column. Only
# variants that passed Verify land here. Field IDs and types match
# VARIANTS exactly so consumers can union-read both tables when needed.
PUBLISHED = Schema(
    NestedField(1,  "variant_id",     StringType(),    required=True),
    NestedField(2,  "source_id",      StringType(),    required=True),
    NestedField(3,  "external_id",    StringType(),    required=True),
    NestedField(4,  "hard_key",       StringType(),    required=True),
    NestedField(5,  "kind",           StringType(),    required=True),
    NestedField(6,  "stage",          StringType(),    required=True),
    NestedField(7,  "title",          StringType(),    required=True),
    NestedField(8,  "issuing_entity", StringType(),    required=False),
    NestedField(9,  "country",        StringType(),    required=False),
    NestedField(10, "region",         StringType(),    required=False),
    NestedField(11, "city",           StringType(),    required=False),
    NestedField(12, "lat",            DoubleType(),    required=False),
    NestedField(13, "lon",            DoubleType(),    required=False),
    NestedField(14, "remote",         BooleanType(),   required=False),
    NestedField(15, "geo_scope",      StringType(),    required=False),
    NestedField(16, "currency",       StringType(),    required=False),
    NestedField(17, "amount_min",     DoubleType(),    required=False),
    NestedField(18, "amount_max",     DoubleType(),    required=False),
    NestedField(19, "deadline",       TimestampType(), required=False),
    NestedField(20, "categories",     StringType(),    required=False),  # comma-joined
    NestedField(21, "attributes",     StringType(),    required=False),  # JSON
    NestedField(22, "scraped_at",     TimestampType(), required=True),
)

# opportunities.crawl_page_completed  (CrawlPageCompletedV1)
CRAWL_PAGE_COMPLETED = Schema(
    _req(1,  "request_id",     StringType()),
    _req(2,  "source_id",      StringType()),
    _opt(3,  "url",            StringType()),
    _opt(4,  "http_status",    IntegerType()),
    _req(5,  "jobs_found",     IntegerType()),
    _req(6,  "jobs_emitted",   IntegerType()),
    _req(7,  "jobs_rejected",  IntegerType()),
    _opt(8,  "cursor",         StringType()),
    _opt(9,  "error_code",     StringType()),
    _opt(10, "error_message",  StringType()),
    *_envelope(11),
)

# opportunities.sources_discovered  (SourceDiscoveredV1)
SOURCES_DISCOVERED = Schema(
    _req(1, "source_id",      StringType()),
    _req(2, "discovered_url", StringType()),
    _opt(3, "name",           StringType()),
    _opt(4, "country",        StringType()),
    _opt(5, "type",           StringType()),
    *_envelope(6),
)

# ---------------------------------------------------------------------------
# candidates namespace
# ---------------------------------------------------------------------------

# candidates.cv_uploaded  (CVUploadedV1)
CV_UPLOADED = Schema(
    _req(1, "candidate_id",    StringType()),
    _req(2, "cv_version",      IntegerType()),
    _req(3, "raw_archive_ref", StringType()),
    _opt(4, "filename",        StringType()),
    _opt(5, "content_type",    StringType()),
    _opt(6, "size_bytes",      LongType()),
    _req(7, "extracted_text",  StringType()),
    *_envelope(8),
)

# candidates.cv_extracted  (CVExtractedV1)
# StrongSkills, WorkingSkills, etc. are []string
CV_EXTRACTED = Schema(
    _req(1,  "candidate_id",            StringType()),
    _req(2,  "cv_version",              IntegerType()),
    _opt(3,  "name",                    StringType()),
    _opt(4,  "email",                   StringType()),
    _opt(5,  "phone",                   StringType()),
    _opt(6,  "location",                StringType()),
    _opt(7,  "current_title",           StringType()),
    _opt(8,  "bio",                     StringType()),
    _opt(9,  "seniority",               StringType()),
    _opt(10, "years_experience",        IntegerType()),
    _opt(11, "primary_industry",        StringType()),
    _opt(12, "strong_skills",           _str_list(element_id=200)),
    _opt(13, "working_skills",          _str_list(element_id=201)),
    _opt(14, "tools_frameworks",        _str_list(element_id=202)),
    _opt(15, "certifications",          _str_list(element_id=203)),
    _opt(16, "preferred_roles",         _str_list(element_id=204)),
    _opt(17, "languages",               _str_list(element_id=205)),
    _opt(18, "education",               StringType()),
    _opt(19, "preferred_locations",     _str_list(element_id=206)),
    _opt(20, "remote_preference",       StringType()),
    _opt(21, "salary_min",              IntegerType()),
    _opt(22, "salary_max",              IntegerType()),
    _opt(23, "currency",                StringType()),
    _req(24, "score_ats",               IntegerType()),
    _req(25, "score_keywords",          IntegerType()),
    _req(26, "score_impact",            IntegerType()),
    _req(27, "score_role_fit",          IntegerType()),
    _req(28, "score_clarity",           IntegerType()),
    _req(29, "score_overall",           IntegerType()),
    _opt(30, "model_version_extract",   StringType()),
    _opt(31, "model_version_score",     StringType()),
    *_envelope(32),
)

# candidates.cv_improved  (CVImprovedV1)
# Fixes is []CVFix — represented as a LIST of STRUCTs
_CV_FIX_STRUCT = StructType(
    NestedField(field_id=300, name="fix_id",          field_type=StringType(),  required=True),
    NestedField(field_id=301, name="title",           field_type=StringType(),  required=False),
    NestedField(field_id=302, name="impact",          field_type=StringType(),  required=False),
    NestedField(field_id=303, name="category",        field_type=StringType(),  required=False),
    NestedField(field_id=304, name="why",             field_type=StringType(),  required=False),
    NestedField(field_id=305, name="auto_applicable", field_type=BooleanType(), required=True),
    NestedField(field_id=306, name="rewrite",         field_type=StringType(),  required=False),
)

CV_IMPROVED = Schema(
    _req(1, "candidate_id",  StringType()),
    _req(2, "cv_version",    IntegerType()),
    _req(3, "fixes",         ListType(element_id=307, element_type=_CV_FIX_STRUCT, element_required=False)),
    _opt(4, "model_version", StringType()),
    *_envelope(5),
)

# candidates.preferences  (PreferencesUpdatedV1)
#
# OptIns is a kind-keyed map of opaque per-kind preference blobs (see
# pkg/events/v1/candidates.go). We persist it as a single JSON string
# (opt_ins_json) so adding a new kind never requires an Iceberg schema
# migration. Readers json.Unmarshal opt_ins_json into
# map[string]json.RawMessage and decode the kind they care about.
PREFERENCES = Schema(
    _req(1, "candidate_id", StringType()),
    _opt(2, "opt_ins_json", StringType()),
    _req(3, "updated_at",   TimestamptzType()),
    *_envelope(4),
)

# candidates.embeddings  (CandidateEmbeddingV1)
CANDIDATE_EMBEDDINGS = Schema(
    _req(1, "candidate_id",  StringType()),
    _req(2, "cv_version",    IntegerType()),
    _req(3, "vector",        _float_list(element_id=500)),
    _opt(4, "model_version", StringType()),
    *_envelope(5),
)

# candidates.matches_ready  (MatchesReadyV1)
# Matches is []MatchRow — LIST of STRUCTs
_MATCH_ROW_STRUCT = StructType(
    NestedField(field_id=600, name="canonical_id",  field_type=StringType(),  required=True),
    NestedField(field_id=601, name="score",         field_type=DoubleType(),  required=True),
    NestedField(field_id=602, name="rerank_score",  field_type=DoubleType(),  required=False),
)

MATCHES_READY = Schema(
    _req(1, "candidate_id",   StringType()),
    _req(2, "match_batch_id", StringType()),
    _req(3, "matches",        ListType(element_id=603, element_type=_MATCH_ROW_STRUCT, element_required=False)),
    *_envelope(4),
)
