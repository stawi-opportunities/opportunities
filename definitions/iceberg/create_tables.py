#!/usr/bin/env python3
"""
Idempotent: create all 11 stawi.jobs Iceberg tables.

Run after create_namespaces.py.  Each create_* function is self-contained —
modify or add individual tables without touching the rest of the script.

Partition specs, sort orders, and bloom-filter properties are sized for
scale: target ~1 M rows per bucket at 10 M canonical jobs.

Table count history:
  14 → 11 (2026-04-22): dropped jobs.canonicals, jobs.canonicals_expired,
           jobs.translations — body now lives in R2 slug-direct JSON;
           materializer subscribes to Frame topics directly.
"""

import os
import sys
from pyiceberg.catalog import Catalog, load_catalog
from pyiceberg.partitioning import PartitionSpec, PartitionField
from pyiceberg.transforms import DayTransform, BucketTransform, IdentityTransform
from pyiceberg.table.sorting import (
    SortOrder,
    SortField,
    NullOrder,
    SortDirection,
)

from _schemas import (
    VARIANTS,
    EMBEDDINGS,
    PUBLISHED,
    CRAWL_PAGE_COMPLETED,
    SOURCES_DISCOVERED,
    CV_UPLOADED,
    CV_EXTRACTED,
    CV_IMPROVED,
    PREFERENCES,
    CANDIDATE_EMBEDDINGS,
    MATCHES_READY,
)

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

# Table properties applied to every table.
_BASE_PROPS: dict[str, str] = {
    "write.target-file-size-bytes":          "134217728",   # 128 MiB
    "write.parquet.compression-codec":        "zstd",
    "write.parquet.compression-level":        "3",
    "write.parquet.page-size-bytes":          "1048576",    # 1 MiB
    "write.parquet.row-group-size-bytes":     "134217728",
    "write.parquet.dict-size-bytes":          "2097152",
    "write.metadata.delete-after-commit.enabled": "true",
    "write.metadata.previous-versions-max":  "100",
    "commit.retry.num-retries":               "5",
    "commit.retry.min-wait-ms":              "100",
    "commit.retry.max-wait-ms":              "10000",
    "history.expire.max-snapshot-age-ms":    "1209600000",  # 14 days
    "history.expire.min-snapshots-to-keep":  "100",
    "write.distribution-mode":               "hash",
    "format-version":                        "2",
}


def _bloom(*cols: str) -> dict[str, str]:
    """Return bloom-filter enable properties for the given column names."""
    return {
        f"write.parquet.bloom-filter-enabled.column.{c}": "true"
        for c in cols
    }


def _props(*bloom_cols: str) -> dict[str, str]:
    return {**_BASE_PROPS, **_bloom(*bloom_cols)}


def _create(
    cat: Catalog,
    identifier: str,
    schema,
    partition_spec: PartitionSpec,
    sort_order: SortOrder,
    bloom_cols: tuple[str, ...] = (),
) -> None:
    """Create table idempotently; skip if it already exists."""
    try:
        cat.create_table(
            identifier=identifier,
            schema=schema,
            partition_spec=partition_spec,
            sort_order=sort_order,
            properties=_props(*bloom_cols),
        )
        print(f"created  {identifier}")
    except Exception as e:
        msg = str(e).lower()
        if "already exists" in msg or "tableexists" in msg or "already exist" in msg:
            print(f"exists   {identifier} — skipping")
        else:
            raise


def _sort_field(schema, col: str, direction: SortDirection = SortDirection.ASC) -> SortField:
    field = schema.find_field(col)
    return SortField(
        source_id=field.field_id,
        transform=IdentityTransform(),
        null_order=NullOrder.NULLS_LAST,
        direction=direction,
    )


def _partition_days(schema, col: str) -> PartitionField:
    field = schema.find_field(col)
    return PartitionField(
        source_id=field.field_id,
        field_id=1000 + field.field_id,
        transform=DayTransform(),
        name=f"{col}_day",
    )


def _partition_bucket(schema, col: str, n: int, pid_offset: int = 2000) -> PartitionField:
    field = schema.find_field(col)
    return PartitionField(
        source_id=field.field_id,
        field_id=pid_offset + field.field_id,
        transform=BucketTransform(n),
        name=f"{col}_bucket_{n}",
    )


# ---------------------------------------------------------------------------
# jobs namespace
# ---------------------------------------------------------------------------

def create_variants(cat: Catalog) -> None:
    """jobs.variants — VariantIngestedV1"""
    schema = VARIANTS
    spec = PartitionSpec(
        _partition_days(schema, "occurred_at"),
        _partition_bucket(schema, "source_id", 32),
    )
    sort = SortOrder(
        _sort_field(schema, "posted_at", SortDirection.DESC),
        _sort_field(schema, "variant_id"),
    )
    _create(cat, "jobs.variants", schema, spec, sort, bloom_cols=("variant_id", "hard_key"))


def create_embeddings(cat: Catalog) -> None:
    """jobs.embeddings — EmbeddingV1 (all embedding events)"""
    schema = EMBEDDINGS
    spec = PartitionSpec(
        _partition_bucket(schema, "canonical_id", 32),
    )
    sort = SortOrder(
        _sort_field(schema, "canonical_id"),
    )
    _create(cat, "jobs.embeddings", schema, spec, sort, bloom_cols=("canonical_id",))



def create_published(cat: Catalog) -> None:
    """jobs.published — PublishedV1"""
    schema = PUBLISHED
    spec = PartitionSpec(
        _partition_days(schema, "occurred_at"),
    )
    sort = SortOrder(
        _sort_field(schema, "published_at"),
        _sort_field(schema, "canonical_id"),
    )
    _create(cat, "jobs.published", schema, spec, sort, bloom_cols=("canonical_id", "slug"))


def create_crawl_page_completed(cat: Catalog) -> None:
    """jobs.crawl_page_completed — CrawlPageCompletedV1"""
    schema = CRAWL_PAGE_COMPLETED
    spec = PartitionSpec(
        _partition_days(schema, "occurred_at"),
        _partition_bucket(schema, "source_id", 16),
    )
    sort = SortOrder(
        _sort_field(schema, "source_id"),
        _sort_field(schema, "occurred_at"),
    )
    _create(cat, "jobs.crawl_page_completed", schema, spec, sort, bloom_cols=("request_id", "source_id"))


def create_sources_discovered(cat: Catalog) -> None:
    """jobs.sources_discovered — SourceDiscoveredV1"""
    schema = SOURCES_DISCOVERED
    spec = PartitionSpec(
        _partition_days(schema, "occurred_at"),
    )
    sort = SortOrder(
        _sort_field(schema, "occurred_at"),
        _sort_field(schema, "discovered_url"),
    )
    # No high-cardinality bloom targets for this table
    _create(cat, "jobs.sources_discovered", schema, spec, sort)


# ---------------------------------------------------------------------------
# candidates namespace
# ---------------------------------------------------------------------------

def create_cv_uploaded(cat: Catalog) -> None:
    """candidates.cv_uploaded — CVUploadedV1"""
    schema = CV_UPLOADED
    spec = PartitionSpec(
        _partition_days(schema, "occurred_at"),
        _partition_bucket(schema, "candidate_id", 32),
    )
    sort = SortOrder(
        _sort_field(schema, "candidate_id"),
        _sort_field(schema, "cv_version"),
    )
    _create(cat, "candidates.cv_uploaded", schema, spec, sort, bloom_cols=("candidate_id",))


def create_cv_extracted(cat: Catalog) -> None:
    """candidates.cv_extracted — CVExtractedV1 (all extract events)"""
    schema = CV_EXTRACTED
    spec = PartitionSpec(
        _partition_days(schema, "occurred_at"),
        _partition_bucket(schema, "candidate_id", 32),
    )
    sort = SortOrder(
        _sort_field(schema, "candidate_id"),
        _sort_field(schema, "cv_version"),
    )
    _create(cat, "candidates.cv_extracted", schema, spec, sort, bloom_cols=("candidate_id",))



def create_cv_improved(cat: Catalog) -> None:
    """candidates.cv_improved — CVImprovedV1"""
    schema = CV_IMPROVED
    spec = PartitionSpec(
        _partition_days(schema, "occurred_at"),
        _partition_bucket(schema, "candidate_id", 32),
    )
    sort = SortOrder(
        _sort_field(schema, "candidate_id"),
        _sort_field(schema, "cv_version"),
    )
    _create(cat, "candidates.cv_improved", schema, spec, sort, bloom_cols=("candidate_id",))


def create_preferences(cat: Catalog) -> None:
    """candidates.preferences — PreferencesUpdatedV1 (all events)"""
    schema = PREFERENCES
    spec = PartitionSpec(
        _partition_bucket(schema, "candidate_id", 32),
    )
    sort = SortOrder(
        _sort_field(schema, "candidate_id"),
    )
    _create(cat, "candidates.preferences", schema, spec, sort, bloom_cols=("candidate_id",))



def create_candidate_embeddings(cat: Catalog) -> None:
    """candidates.embeddings — CandidateEmbeddingV1 (all events)"""
    schema = CANDIDATE_EMBEDDINGS
    spec = PartitionSpec(
        _partition_bucket(schema, "candidate_id", 32),
    )
    sort = SortOrder(
        _sort_field(schema, "candidate_id"),
    )
    _create(cat, "candidates.embeddings", schema, spec, sort, bloom_cols=("candidate_id",))



def create_matches_ready(cat: Catalog) -> None:
    """candidates.matches_ready — MatchesReadyV1"""
    schema = MATCHES_READY
    spec = PartitionSpec(
        _partition_days(schema, "occurred_at"),
        _partition_bucket(schema, "candidate_id", 32),
    )
    sort = SortOrder(
        _sort_field(schema, "candidate_id"),
        _sort_field(schema, "match_batch_id"),
    )
    _create(cat, "candidates.matches_ready", schema, spec, sort, bloom_cols=("candidate_id", "match_batch_id"))


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------

_ALL_CREATORS = [
    # jobs (5 tables)
    create_variants,
    create_embeddings,
    create_published,
    create_crawl_page_completed,
    create_sources_discovered,
    # candidates (6 tables)
    create_cv_uploaded,
    create_cv_extracted,
    create_cv_improved,
    create_preferences,
    create_candidate_embeddings,
    create_matches_ready,
]
assert len(_ALL_CREATORS) == 11, f"Expected 11 Iceberg tables, got {len(_ALL_CREATORS)}"


def main() -> None:
    cat = load_catalog("stawi", **{
        "type": "sql",
        "uri": os.environ["ICEBERG_CATALOG_URI"],
        "s3.endpoint": os.environ.get("R2_ENDPOINT", ""),
        "s3.access-key-id": os.environ["R2_ACCESS_KEY_ID"],
        "s3.secret-access-key": os.environ["R2_SECRET_ACCESS_KEY"],
        "s3.region": os.environ.get("R2_REGION", "auto"),
        "warehouse": f"s3://{os.environ['R2_LOG_BUCKET']}/iceberg",
    })

    errors: list[tuple[str, Exception]] = []
    for fn in _ALL_CREATORS:
        try:
            fn(cat)
        except Exception as exc:
            name = fn.__name__.replace("create_", "")
            print(f"ERROR    {name}: {exc}", file=sys.stderr)
            errors.append((name, exc))

    if errors:
        print(f"\n{len(errors)} table(s) failed — see errors above.", file=sys.stderr)
        sys.exit(1)

    print(f"\nDone — {len(_ALL_CREATORS)} tables processed (target: 11).")


if __name__ == "__main__":
    main()
