"""Authoritative table registry. Mirrors definitions/iceberg/_schemas.py."""

HOT_TABLES = [
    # High-write-rate tables — compact hourly to keep the small-file
    # problem bounded.
    "jobs.variants",
    "jobs.canonicals",
    "jobs.embeddings",
]

ALL_TABLES = [
    "jobs.variants",
    "jobs.canonicals",
    "jobs.canonicals_current",
    "jobs.canonicals_expired",
    "jobs.embeddings",
    "jobs.embeddings_current",
    "jobs.translations",
    "jobs.translations_current",
    "jobs.published",
    "jobs.crawl_page_completed",
    "jobs.sources_discovered",
    "candidates.cv_uploaded",
    "candidates.cv_extracted",
    "candidates.cv_extracted_current",
    "candidates.cv_improved",
    "candidates.preferences",
    "candidates.preferences_current",
    "candidates.embeddings",
    "candidates.embeddings_current",
    "candidates.matches_ready",
]

# Tables rebuilt from their append-only source via MERGE INTO semantics.
# Tuples of (source, target, merge_key) where merge_key is a str (single
# column) or tuple[str, ...] (composite key).
CURRENT_TABLE_MERGES = [
    ("jobs.canonicals",         "jobs.canonicals_current",         "cluster_id"),
    ("jobs.embeddings",         "jobs.embeddings_current",         "canonical_id"),
    ("jobs.translations",       "jobs.translations_current",       ("canonical_id", "lang")),
    ("candidates.cv_extracted", "candidates.cv_extracted_current", "candidate_id"),
    ("candidates.preferences",  "candidates.preferences_current",  "candidate_id"),
    ("candidates.embeddings",   "candidates.embeddings_current",   "candidate_id"),
]
