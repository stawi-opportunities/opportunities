# First deployment

Production topology: **two Neon projects** + cluster crawl jobs.  
See [db-boundaries.md](./db-boundaries.md).

1. Provision **product Neon** via Cloud Run `opportunities-matching` (`DO_SETUP` / migrate).
2. Provision **crawl Neon** via Cloud Run `opportunities-crawler` (`DO_DATABASE_MIGRATE`).
3. Deploy **api** (Cloud Run) sharing the **product** Neon secret.
4. Seed cluster secrets:
   - `crawl-neon-credentials-opportunities` ← crawl Neon URL
   - `product-neon-credentials-opportunities` ← product Neon URL
5. Deploy **worker** with both URLs + `MATCHING_FANOUT_QUEUE_URL`.
6. Deploy **crawler** + **frontier-worker** (crawl Neon only).
7. Sync Trustage workflows when configured on crawler.
8. Confirm worker health before enabling schedules.
9. Confirm crawler `/admin/crawl/status` is healthy when schedules should run.
10. Enable per-source schedules gradually.

## Required consistency

Set the same values on crawler, frontier-worker, and worker:

- `INGEST_MAX_PENDING`
- `INGEST_MAX_OLDEST_AGE`

Worker production:

- `DATABASE_URL` → crawl Neon
- `PRODUCT_DATABASE_URL` → product Neon (required)
- `MATCHING_FANOUT_QUEUE_URL` → Pub/Sub fan-out topic

## Extraction

No crawl-time AI env is required for job extract. Optional LLM env on **crawler** is only for recipe generation.

## Do not

- Use a single Neon project for crawl + product.
- Point crawler migrate at product Neon or matching migrate at crawl Neon.
- Deploy retired cluster apps (materializer, writer, multi-stage workers, cluster api/matching).
- Reintroduce URL-stub / universal AI extract paths.
