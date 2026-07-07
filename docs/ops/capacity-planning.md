# Capacity planning

Worker throughput must exceed sustained crawl production. PostgreSQL enforces a
hard outstanding-work ceiling with `INGEST_MAX_PENDING`; crawl admission also
stops when `INGEST_MAX_OLDEST_AGE` is exceeded.

Scale workers from observed processing latency and queue age. Increase the
queue ceiling only when PostgreSQL storage, WAL, autovacuum, and worker capacity
have been measured under the intended load. Queue depth alone is insufficient:
oldest age is the service-level signal.

TimescaleDB retention and compression apply to append-only ingestion events.
The mutable leased queue remains a regular PostgreSQL table.
