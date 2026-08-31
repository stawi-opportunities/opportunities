# Integration tests

Integration tests use `timescale/timescaledb-ha:pg16` through testcontainers —
only because pre-2026-08-31 capability migrations unconditionally create the
timescaledb extension when the full history is replayed. The 20260831 removal
migrations then convert everything to plain PostgreSQL tables and drop the
extension, and the tests assert that plain end state. They validate PostgreSQL
migrations, leased queue behavior, canonical identity, source reconciliation,
and append-only trigger guards.

Run the ingestion suite with:

```bash
go test -tags=integration -count=1 -timeout=5m ./pkg/jobqueue
```
