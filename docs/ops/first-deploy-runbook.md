# First deployment

1. Provision PostgreSQL with **TimescaleDB** and **pgvector**.
2. Deploy **crawler** and **matching** with `DO_DATABASE_MIGRATE=true`; wait until both migration jobs exit successfully.
3. Deploy **worker** (and **frontier-worker** if any source uses the URL frontier).
4. Deploy **api**.
5. Sync Trustage workflows from `definitions/trustage/` (crawler migration job can do this when `TRUSTAGE_URL` + `TRUSTAGE_WORKFLOWS_DIR` are set).
6. Confirm worker health before enabling schedules: queue should drain, not pile up.
7. Confirm crawler `/admin/crawl/status` is healthy (`paused=false` when schedules should run).
8. Enable per-source schedules gradually; watch `pending` and `oldest_age_seconds`.

## Required consistency

Set the same values on crawler, frontier-worker, and worker:

- `INGEST_MAX_PENDING`
- `INGEST_MAX_OLDEST_AGE`

## Extraction

No crawl-time AI env is required for job extract. Optional LLM env on **crawler** is only for recipe generation (`RECIPE_ENABLED`, inference base URL).

Matching needs its own inference/embed env for CV processing when that product path is live.

## Do not

- Enable crawl schedules before workers are up.
- Reintroduce URL-stub / universal AI extract paths.
