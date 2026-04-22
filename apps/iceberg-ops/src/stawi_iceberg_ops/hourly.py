"""Hot-table hourly compaction. Short run (target <5 min)."""
import logging
import os
import sys
from concurrent.futures import ThreadPoolExecutor, as_completed

from .catalog import load_catalog
from .tables import HOT_TABLES

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
log = logging.getLogger("iceberg-ops.hourly")

TARGET_FILE_SIZE = int(os.environ.get("ICEBERG_TARGET_FILE_SIZE", str(128 * 1024 * 1024)))


def compact_one(cat, ident: str) -> dict:
    try:
        t = cat.load_table(ident)
        before = sum(1 for _ in t.scan().plan_files())
        t.rewrite_data_files(target_file_size_bytes=TARGET_FILE_SIZE)
        after = sum(1 for _ in t.refresh().scan().plan_files())
        log.info("compacted %s: %d -> %d files", ident, before, after)
        return {"table": ident, "files_before": before, "files_after": after}
    except Exception as e:  # noqa: BLE001 — isolate per-table failures
        log.exception("compaction failed for %s", ident)
        return {"table": ident, "error": str(e)}


def main() -> int:
    cat = load_catalog()
    results = []
    with ThreadPoolExecutor(max_workers=3) as pool:
        futures = [pool.submit(compact_one, cat, t) for t in HOT_TABLES]
        for f in as_completed(futures):
            results.append(f.result())
    errs = [r for r in results if "error" in r]
    log.info("hourly done: %d ok, %d failed", len(results) - len(errs), len(errs))
    if errs:
        for r in errs:
            log.error(r)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
