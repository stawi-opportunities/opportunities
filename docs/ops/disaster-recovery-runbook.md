# PostgreSQL disaster recovery

Restore the PostgreSQL/TimescaleDB cluster from its managed backup and replay
WAL to the selected recovery point. Then:

1. Start API and migration owners.
2. Verify `opportunities`, `job_ingest_queue`, `opportunity_sources`, and
   `job_ingest_events` are readable.
3. Start workers. Expired processing leases are reclaimed automatically.
4. Confirm queue depth and oldest age are falling via `/admin/crawl/status`.
5. Start crawlers only after processing capacity is stable.

Pending and retry work must not be deleted during recovery. A full source crawl
is safe to repeat because queue admission and canonical identity are idempotent.
