# Capacity planning

Worker throughput must exceed sustained crawl production. PostgreSQL enforces a hard outstanding-work ceiling with `INGEST_MAX_PENDING`; crawl admission also stops when `INGEST_MAX_OLDEST_AGE` is exceeded.

## Signals

| Signal | Meaning |
|--------|---------|
| `pending` queue depth | Work waiting for workers |
| `oldest_age_seconds` | Service-level lag — primary SLO signal |
| Crawl admit 429 / Retry-After | Producers correctly back-pressured |
| Verify rejections | Extract quality, not capacity (fix connectors/recipes) |

## Scaling

- Scale **workers** from observed processing latency and oldest age.
- Increase `INGEST_MAX_PENDING` only when Postgres storage, WAL, autovacuum, and worker capacity have been measured under intended load.
- Queue depth alone is insufficient; **oldest age** is the service-level signal.

## Crawl cost profile

Structured extract paths (JSON APIs, schema.org JSON-LD) are CPU/network bound and predictable. Recipe generation (optional LLM) is offline/ops and should not share the hot crawl path's SLO.

## Event storage

Append-only ingestion events accumulate in plain PostgreSQL tables — there is no automatic retention or compression. Revisit partitioning/retention with pg_partman if volume warrants. The mutable leased queue remains a regular PostgreSQL table.
