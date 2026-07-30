# Crawl Neon database

PostgreSQL (Neon) is the durable store for crawl control and ingest queues.

GORM models own ordinary tables. SQL is reserved for capabilities GORM cannot
express (partial indexes, optional Timescale hypertables, append-only triggers).

**Owned here (crawl plane):**

- `sources`, `source_recipes`, `crawl_runs`, `host_state`
- `url_frontier`
- `job_ingest_queue`, `job_ingest_events`
- `crawl_jobs`

**Not owned here:** product catalog (`opportunities`, candidates, matching).
Those migrate via `apps/matching` against product Neon.

Timescale compression / retention / `add_job` are soft-failed on Neon Apache-2.
