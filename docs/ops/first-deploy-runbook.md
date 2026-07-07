# First deployment

1. Provision PostgreSQL with the TimescaleDB and pgvector extensions.
2. Deploy crawler and matching with `DO_DATABASE_MIGRATE=true`.
3. Wait for both migration processes to finish successfully.
4. Deploy API, worker, and frontier-worker.
5. Confirm `/admin/crawl/status` reports `paused=false`.
6. Enable crawl schedules gradually and watch `pending` and
   `oldest_age_seconds`.

Do not enable crawlers before workers are healthy. Configure
`INGEST_MAX_PENDING` and `INGEST_MAX_OLDEST_AGE` on crawler, frontier-worker,
and worker consistently.
