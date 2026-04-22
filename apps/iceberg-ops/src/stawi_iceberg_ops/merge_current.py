"""Rebuild _current Iceberg tables from their append-only source tables.

PyIceberg 0.8 does not expose a SQL MERGE INTO helper. We implement the
equivalent by:
  1. Scan the source table, deduplicate by key keeping the row with the
     latest occurred_at value.
  2. Atomically overwrite the entire _current table via Table.overwrite()
     which defaults to overwrite_filter=AlwaysTrue() — a single Iceberg
     snapshot commit. Readers never see a partial state.

Verified against pyiceberg 0.8.1:
  Table.overwrite(df: pa.Table, overwrite_filter=AlwaysTrue(), ...) -> None
The AlwaysTrue default means the overwrite replaces ALL rows atomically.
No fallback to delete+append is required.
"""
import logging
from typing import Union

import pyarrow as pa

log = logging.getLogger("iceberg-ops.merge_current")


def _dedup_latest(tbl: pa.Table, key_cols: tuple[str, ...]) -> pa.Table:
    """For each unique key tuple keep the row with the greatest occurred_at."""
    # Sort ascending by key, then descending by occurred_at so that
    # drop_duplicates(keep="first") retains the latest row per key.
    sort_keys = [(k, "ascending") for k in key_cols] + [("occurred_at", "descending")]
    sorted_tbl = tbl.sort_by(sort_keys)
    df = sorted_tbl.to_pandas()
    deduped = df.drop_duplicates(subset=list(key_cols), keep="first")
    return pa.Table.from_pandas(deduped, schema=sorted_tbl.schema, preserve_index=False)


def merge_current(
    cat,
    source: str,
    target: str,
    key: Union[str, tuple[str, ...]],
) -> dict:
    """Atomically rebuild *target* from *source* by deduplicating on *key*.

    Uses Table.overwrite(df) which defaults to AlwaysTrue() — one snapshot
    commit that replaces all rows. Safe to retry (idempotent).
    """
    try:
        key_cols: tuple[str, ...] = (key,) if isinstance(key, str) else key

        src_arrow = cat.load_table(source).scan().to_arrow()
        deduped = _dedup_latest(src_arrow, key_cols)

        tgt = cat.load_table(target)
        # overwrite_filter defaults to AlwaysTrue() — replaces ALL rows
        # atomically in a single Iceberg snapshot commit.
        tgt.overwrite(deduped)

        log.info("merged %s -> %s: %d rows", source, target, deduped.num_rows)
        return {"merge": f"{source}->{target}", "rows": deduped.num_rows}
    except Exception as e:  # noqa: BLE001 — isolate per-merge failures
        log.exception("merge failed %s -> %s", source, target)
        return {"merge": f"{source}->{target}", "error": str(e)}


def run_all_current_merges(cat) -> list[dict]:
    """Run all CURRENT_TABLE_MERGES sequentially. Returns list of result dicts."""
    from .tables import CURRENT_TABLE_MERGES

    return [merge_current(cat, src, tgt, key) for src, tgt, key in CURRENT_TABLE_MERGES]
