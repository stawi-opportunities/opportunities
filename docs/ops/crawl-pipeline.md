# Crawl pipeline

The opportunity pipeline has one durable processing boundary: PostgreSQL.

```text
Trustage source schedule
  -> crawl request
  -> resumable connector/recipe pages
  -> verify and normalize source record
  -> job_ingest_queue
  -> PostgreSQL worker
  -> opportunities + opportunity_sources
  -> PostgreSQL search/detail API
```

## Backpressure

`INGEST_MAX_PENDING` is a hard producer credit limit. Crawler replicas reserve
capacity under a PostgreSQL transaction advisory lock, so concurrent producers
cannot exceed it. `INGEST_MAX_OLDEST_AGE` closes crawl admission when the oldest
unfinished item exceeds the processing-time budget. The URL frontier checks the
same limits before it fetches another page.

When admission closes, a crawl run yields without advancing past an incomplete
page. Previously inserted rows are idempotent, so the watchdog can resume the
same page after capacity becomes available without loss or duplication.

## Processing

Workers claim batches with `FOR UPDATE SKIP LOCKED` and a bounded lease. Expired
leases are reclaimable. Identity resolution, canonical merge, source-lineage
update, queue acknowledgement, and the append-only processed event commit in a
single transaction. Failures use bounded exponential retry and end in `dead`;
they are never silently acknowledged.

## Full-crawl reconciliation

After an iterator is exhausted, source lineage not observed since that run
started becomes inactive. An opportunity is hidden only when it has no active
source. Delayed work from an older run cannot reactivate stale lineage because
each lineage row records its reconciliation boundary.

## TimescaleDB usage

`job_ingest_events` is append-only and protected by database triggers against
update, delete, and truncate. It uses daily chunks, seven-day compression, and
90-day retention. Mutable queue and canonical tables remain ordinary
PostgreSQL tables.

Raw HTTP bodies are parsed in memory and discarded. Only parsed queue records,
canonical opportunities, compact lineage, and operational events are persisted.
