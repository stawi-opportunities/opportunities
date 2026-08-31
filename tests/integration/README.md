# Integration tests

Integration tests use `pgvector/pgvector:pg16` through testcontainers —
historical capability migrations soft-fail their `CREATE EXTENSION timescaledb`
on plain PostgreSQL, so the full history replays cleanly without the
timescaledb-ha image; only the vector extension is required. The tests assert
the plain PostgreSQL + pgvector end state. They validate PostgreSQL
migrations, leased queue behavior, canonical identity, source reconciliation,
and append-only trigger guards.

Run the ingestion suite with:

```bash
go test -tags=integration -count=1 -timeout=5m ./pkg/jobqueue
```
