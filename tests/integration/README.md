# Integration Tests

Gate: `//go:build integration` — not run by `go test ./...` by default.

## What's covered

| Test | Containers | What it verifies |
|------|-----------|-----------------|
| `TestMaterializerManticoreE2E` | Manticore | canonical event → searchindex.Client.Replace → idx_jobs_rt row present |
| `TestMaterializerManticoreSchemaIdempotent` | Manticore | Apply called twice → no error |

## What's not yet covered (TODO)

- Full NATS JetStream round-trip: Frame Publish → consumer group → handler Execute
- Writer: VariantIngested → Iceberg Parquet flush to MinIO
- Worker: TopicCanonicalsUpserted → R2 slug JSON
- Valkey dedup
- Postgres catalog

These require more container wiring that can be added incrementally to `testhelpers/`.

## How to run

```bash
# Prerequisites: Docker running locally
docker pull manticoresearch/manticore:6.2.0  # cache the image

# Run all integration tests
go test -tags integration -v -timeout 5m ./tests/integration/...

# Run a specific test
go test -tags integration -v -run TestMaterializerManticoreE2E ./tests/integration/...

# Run with race detector
go test -tags integration -race -timeout 5m ./tests/integration/...
```

## Compile check (no Docker required)

```bash
go test -tags integration -run=none ./tests/integration/...
```

## Known failure modes

| Symptom | Cause | Fix |
|---------|-------|-----|
| `cannot start container` | Docker not running | Start Docker |
| `image not found locally` | manticoresearch image not pulled | `docker pull manticoresearch/manticore:6.2.0` |
| `deadline exceeded` during startup | Slow image pull on first run | Increase timeout in `testhelpers/containers.go` |
| `table already exists` error | Schema already applied in this container | Safe to ignore — `Apply` handles it |
| `connection refused` to Manticore | Container not healthy yet | The test uses `require.Eventually` — extend the timeout |

## Container images

| Service | Image | Port |
|---------|-------|------|
| Manticore | `manticoresearch/manticore:6.2.0` | 9308 (HTTP JSON API) |
| MinIO | `minio/minio:latest` | 9000 |
| NATS | `nats:2.10-alpine` | 4222 |
| Valkey | `valkey/valkey:7.2-alpine` | 6379 |
| Postgres | `postgres:16-alpine` | 5432 |

All containers are started and stopped per test via `t.Cleanup`.

## Architecture notes

The current tests drive the materializer's internal handler directly via
`searchindex.Client.Replace`, bypassing NATS. This is intentional for the
initial skeleton — it covers the critical schema + write path without requiring
a full NATS JetStream stream configuration.

To extend to a full Frame→handler test:
1. Add a `NATSContainer` helper (already in `testhelpers/containers.go`)
2. Create the `svc_stawi_jobs_events` JetStream stream in the test setup
3. Use `frame.NewServiceWithContext` with the NATS container URL as `EVENTS_QUEUE_URL`
4. Publish via `frame.Publish` and assert the handler was called
