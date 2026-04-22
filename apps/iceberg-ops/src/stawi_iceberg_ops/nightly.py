"""Nightly maintenance: compact everything + rewrite manifests + expire
snapshots + MERGE INTO _current tables."""
import logging
import os
import sys
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import datetime, timedelta, timezone

from .catalog import load_catalog
from .merge_current import run_all_current_merges
from .tables import APPEND_ONLY_TABLES

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
log = logging.getLogger("iceberg-ops.nightly")

TARGET_FILE_SIZE = int(os.environ.get("ICEBERG_TARGET_FILE_SIZE", str(128 * 1024 * 1024)))
SNAPSHOT_RETENTION_DAYS = int(os.environ.get("ICEBERG_SNAPSHOT_RETENTION_DAYS", "14"))
MIN_SNAPSHOTS = int(os.environ.get("ICEBERG_MIN_SNAPSHOTS_TO_KEEP", "100"))


def maintain_one(cat, ident: str) -> dict:
    try:
        t = cat.load_table(ident)
        before = sum(1 for _ in t.scan().plan_files())

        t.rewrite_data_files(target_file_size_bytes=TARGET_FILE_SIZE)
        t = t.refresh()

        t.rewrite_manifests()
        t = t.refresh()

        cutoff_ms = int(
            (datetime.now(timezone.utc) - timedelta(days=SNAPSHOT_RETENTION_DAYS)).timestamp() * 1000
        )
        t.expire_snapshots(
            older_than_ms=cutoff_ms,
            min_snapshots_to_keep=MIN_SNAPSHOTS,
        )

        after = sum(1 for _ in t.refresh().scan().plan_files())
        log.info("maintained %s: %d -> %d files", ident, before, after)
        return {"table": ident, "files_before": before, "files_after": after}
    except Exception as e:  # noqa: BLE001 — isolate per-table failures
        log.exception("maintenance failed for %s", ident)
        return {"table": ident, "error": str(e)}


def main() -> int:
    cat = load_catalog()
    results = []
    with ThreadPoolExecutor(max_workers=4) as pool:
        futures = [pool.submit(maintain_one, cat, t) for t in APPEND_ONLY_TABLES]
        for f in as_completed(futures):
            results.append(f.result())

    merge_results = run_all_current_merges(cat)
    results.extend(merge_results)

    errs = [r for r in results if "error" in r]
    log.info("nightly done: %d ok, %d failed", len(results) - len(errs), len(errs))
    if errs:
        for r in errs:
            log.error(r)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
