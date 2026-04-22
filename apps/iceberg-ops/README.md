# stawi-jobs iceberg-ops

Hourly + nightly maintenance for the stawi.jobs Iceberg catalog. Runs as
two Kubernetes CronJobs in the `stawi-jobs` namespace.

## Cadences

- **Hourly** (`python -m stawi_iceberg_ops.hourly`): compact only the
  hot tables (variants, canonicals, embeddings) to bound the small-file
  problem between nightly runs.
- **Nightly** (`python -m stawi_iceberg_ops.nightly`): full compaction +
  manifest rewrite + snapshot expiry for every table, then MERGE INTO
  rebuilds for every `_current` table.

## Environment

| Var | Purpose |
|---|---|
| `ICEBERG_CATALOG_URI` | `postgresql+psycopg2://user:pass@host:5432/stawi_jobs` |
| `R2_ACCESS_KEY_ID` / `R2_SECRET_ACCESS_KEY` | R2 credentials for the log bucket |
| `R2_LOG_BUCKET` | e.g. `stawi-jobs-log` |
| `R2_ENDPOINT` | e.g. `https://<account>.r2.cloudflarestorage.com` |
| `R2_REGION` | `auto` |
| `ICEBERG_TARGET_FILE_SIZE` | default `134217728` (128 MiB) |
| `ICEBERG_SNAPSHOT_RETENTION_DAYS` | default `14` |
| `ICEBERG_MIN_SNAPSHOTS_TO_KEEP` | default `100` |

## Local dev

    pip install -e .
    pip install -r requirements.txt
    export ICEBERG_CATALOG_URI=postgresql+psycopg2://...
    export R2_ACCESS_KEY_ID=...
    export R2_SECRET_ACCESS_KEY=...
    export R2_LOG_BUCKET=stawi-jobs-log
    export R2_ENDPOINT=https://<account>.r2.cloudflarestorage.com
    python -m stawi_iceberg_ops.nightly
