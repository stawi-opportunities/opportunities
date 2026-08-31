# Crawl Neon database

PostgreSQL (Neon) is the durable store for crawl control and ingest queues.

GORM models own ordinary tables. SQL is reserved for capabilities GORM cannot
express (partial indexes, append-only triggers, materialized views).

**Owned here (crawl plane):**

- `sources`, `source_recipes`, `crawl_runs`, `host_state`
- `url_frontier`
- `job_ingest_queue`, `job_ingest_events`
- `crawl_jobs`

**Not owned here:** product catalog (`opportunities`, candidates, matching).
Those migrate via `apps/matching` against product Neon.

TimescaleDB was removed on 2026-08-31 (`20260831_0023_remove_timescaledb.sql`);
older files reference it behind soft-fail guards and no-op without it.
