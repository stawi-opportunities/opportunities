"""
Shared Iceberg schema definitions for all 19 stawi.jobs tables.

Field IDs start at 1 per table and are assigned in declaration order.
Field names match the `parquet:"..."` struct tags in pkg/events/v1/*.go.
Every table ends with event_id (string, required) and occurred_at
(timestamptz, required) — the Phase 6 Task 0 envelope columns.

Envelope columns are NOT optional — the writer always populates them.
Optional fields from Go structs (parquet:"...,optional") map to
optional() in Iceberg.
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
# jobs namespace
# ---------------------------------------------------------------------------

# jobs.variants  (VariantIngestedV1)
VARIANTS = Schema(
    _req(1,  "variant_id",            StringType()),
    _req(2,  "source_id",             StringType()),
    _req(3,  "external_id",           StringType()),
    _req(4,  "hard_key",              StringType()),
    _req(5,  "stage",                 StringType()),
    _opt(6,  "title",                 StringType()),
    _opt(7,  "company",               StringType()),
    _opt(8,  "location_text",         StringType()),
    _opt(9,  "country",               StringType()),
    _opt(10, "language",              StringType()),
    _opt(11, "remote_type",           StringType()),
    _opt(12, "employment_type",       StringType()),
    _opt(13, "salary_min",            DoubleType()),
    _opt(14, "salary_max",            DoubleType()),
    _opt(15, "currency",              StringType()),
    _opt(16, "description",           StringType()),
    _opt(17, "apply_url",             StringType()),
    _opt(18, "posted_at",             TimestamptzType()),
    # scraped_at is optional: flagged/expired variants carry no scrape
    # timestamp. Ingested/normalized/validated/clustered still populate
    # it. Nullability only means the column ALLOWS null, not requires it.
    # Note: create_tables.py is idempotent; this change takes effect on
    # first table creation (pre-deployment). Existing tables need an
    # ALTER COLUMN scraped_at DROP NOT NULL via SQL catalog migration.
    _opt(19, "scraped_at",            TimestamptzType()),
    _opt(20, "content_hash",          StringType()),
    _opt(21, "raw_archive_ref",       StringType()),
    _opt(22, "model_version_extract", StringType()),
    *_envelope(23),
)

# jobs.canonicals  (CanonicalUpsertedV1)
CANONICALS = Schema(
    _req(1,  "canonical_id",    StringType()),
    _req(2,  "cluster_id",      StringType()),
    _req(3,  "slug",            StringType()),
    _opt(4,  "title",           StringType()),
    _opt(5,  "company",         StringType()),
    _opt(6,  "description",     StringType()),
    _opt(7,  "location_text",   StringType()),
    _opt(8,  "country",         StringType()),
    _opt(9,  "language",        StringType()),
    _opt(10, "remote_type",     StringType()),
    _opt(11, "employment_type", StringType()),
    _opt(12, "seniority",       StringType()),
    _opt(13, "salary_min",      DoubleType()),
    _opt(14, "salary_max",      DoubleType()),
    _opt(15, "currency",        StringType()),
    _opt(16, "category",        StringType()),
    _opt(17, "quality_score",   DoubleType()),
    _req(18, "status",          StringType()),
    _opt(19, "posted_at",       TimestamptzType()),
    _opt(20, "first_seen_at",   TimestamptzType()),
    _opt(21, "last_seen_at",    TimestamptzType()),
    _opt(22, "expires_at",      TimestamptzType()),
    _opt(23, "apply_url",       StringType()),
    *_envelope(24),
)

# jobs.canonicals_expired  (CanonicalExpiredV1)
CANONICALS_EXPIRED = Schema(
    _req(1, "canonical_id", StringType()),
    _opt(2, "cluster_id",   StringType()),
    _opt(3, "reason",       StringType()),
    _req(4, "expired_at",   TimestamptzType()),
    *_envelope(5),
)

# jobs.embeddings  (EmbeddingV1)
# vector is []float32 — ListType of FloatType
EMBEDDINGS = Schema(
    _req(1, "canonical_id",   StringType()),
    _req(2, "vector",         _float_list(element_id=100)),
    _req(3, "model_version",  StringType()),
    *_envelope(4),
)

# jobs.translations  (TranslationV1)
TRANSLATIONS = Schema(
    _req(1, "canonical_id",    StringType()),
    _req(2, "lang",            StringType()),
    _opt(3, "title_tr",        StringType()),
    _opt(4, "description_tr",  StringType()),
    _opt(5, "model_version",   StringType()),
    *_envelope(6),
)

# jobs.published  (PublishedV1)
PUBLISHED = Schema(
    _req(1, "canonical_id", StringType()),
    _req(2, "slug",         StringType()),
    _req(3, "r2_version",   IntegerType()),
    _req(4, "published_at", TimestamptzType()),
    *_envelope(5),
)

# jobs.crawl_page_completed  (CrawlPageCompletedV1)
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

# jobs.sources_discovered  (SourceDiscoveredV1)
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
PREFERENCES = Schema(
    _req(1,  "candidate_id",        StringType()),
    _opt(2,  "remote_preference",   StringType()),
    _opt(3,  "salary_min",          IntegerType()),
    _opt(4,  "salary_max",          IntegerType()),
    _opt(5,  "currency",            StringType()),
    _opt(6,  "preferred_locations", _str_list(element_id=400)),
    _opt(7,  "excluded_companies",  _str_list(element_id=401)),
    _opt(8,  "target_roles",        _str_list(element_id=402)),
    _opt(9,  "languages",           _str_list(element_id=403)),
    _opt(10, "availability",        StringType()),
    *_envelope(11),
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
