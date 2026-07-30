# First deployment

Production topology: **Cloud Run product (Neon)** + **cluster crawl (CNPG)**.  
See [db-boundaries.md](./db-boundaries.md).

1. Provision **crawl CNPG** (TimescaleDB) and **product Neon** (vector + lakebase_text when enabled).
2. Deploy **matching** (Cloud Run) with product migrate (`DO_DATABASE_MIGRATE=true`); wait until success.
3. Deploy **crawler** (cluster) with crawl migrate against CNPG; wait until success.
4. Deploy **worker** with **both** `DATABASE_URL` (crawl) and `PRODUCT_DATABASE_URL` (Neon), plus `MATCHING_FANOUT_QUEUE_URL`.
5. Deploy **frontier-worker** if any source uses the URL frontier (crawl DB only).
6. Deploy **api** (Cloud Run) sharing the product Neon secret.
7. Sync Trustage workflows from `definitions/trustage/` (crawler migration can do this when `TRUSTAGE_URL` + `TRUSTAGE_WORKFLOWS_DIR` are set).
8. Confirm worker health before enabling schedules: queue should drain, not pile up.
9. Confirm crawler `/admin/crawl/status` is healthy (`paused=false` when schedules should run).
10. Enable per-source schedules gradually; watch `pending` and `oldest_age_seconds`.

## Required consistency

Set the same values on crawler, frontier-worker, and worker:

- `INGEST_MAX_PENDING`
- `INGEST_MAX_OLDEST_AGE`

Worker production:

- `DATABASE_URL` → crawl
- `PRODUCT_DATABASE_URL` → product Neon (required)
- `MATCHING_FANOUT_QUEUE_URL` → Pub/Sub fan-out topic

## Extraction

No crawl-time AI env is required for job extract. Optional LLM env on **crawler** is only for recipe generation (`RECIPE_ENABLED`, inference base URL).

Matching needs its own inference/embed env for CV processing when that product path is live.

## Do not

- Enable crawl schedules before workers are up.
- Point crawler migrate at Neon or matching migrate at CNPG.
- Deploy retired cluster apps (materializer, writer, worker-core/validate/publish, cluster api/matching).
- Reintroduce URL-stub / universal AI extract paths.
