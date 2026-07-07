# Integration tests

Integration tests use `timescale/timescaledb-ha:pg16` through testcontainers.
They validate PostgreSQL migrations, leased queue behavior, canonical identity,
source reconciliation, and append-only TimescaleDB guards.

Run the ingestion suite with:

```bash
go test -tags=integration -count=1 -timeout=5m ./pkg/jobqueue
```
