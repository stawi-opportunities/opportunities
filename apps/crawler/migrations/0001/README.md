# Opportunities database

PostgreSQL is the only structured data store for opportunities.

GORM models own ordinary tables, columns, constraints, and conventional
indexes. SQL is reserved for PostgreSQL/TimescaleDB capabilities GORM cannot
represent: extensions, hypertable policies, append-only triggers, partial
indexes, scheduled cleanup, and the crawl-signals materialized view.

- `job_ingest_queue`: mutable, leased work queue. Pending work has no TTL.
- `opportunities`: current canonical rows used by search and detail APIs.
- `opportunity_identities`: atomic `hard_key` to canonical ID mapping.
- `opportunity_sources`: per-source lineage and full-crawl presence state.
- `job_ingest_events`: append-only TimescaleDB audit ledger, compressed after
  seven days and retained for 90 days.
- `crawl_jobs`: time-partitioned operational crawl history.

The queue is intentionally not a hypertable because workers update leases,
attempts, and terminal state. TimescaleDB is limited to time-series records
whose row identity is append-only. Raw HTTP bodies are not persisted.

The single capability migration is executed as one prepared statement, so its
operations live in one `DO` block.
