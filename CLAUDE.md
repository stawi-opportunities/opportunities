# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Dependencies
make deps                  # go mod tidy

# Build & run
make build                 # Compile all apps to bin/
make run-api               # Run search API (port 8082)
make run-worker            # Run job pipeline worker
make run-crawler           # Run connector runtime
make run-scheduler         # Run crawl scheduler
make run-materializer      # Run Iceberg → Manticore materializer
make run-writer            # Run event log writer

# Infrastructure
make infra-up              # Start Postgres, Redis, OpenSearch, Manticore via docker-compose
make infra-down

# Tests
make test                  # go test ./...  (unit tests only)
go test -tags integration -timeout 5m ./tests/integration/...  # integration tests (require Docker)
go test ./pkg/normalize/... # Run a single package's tests

# UI (Hugo + React/Vite)
make ui-deps
make ui-dev                # Dev server
make ui-build              # Production build → ui/static/

# Linting
golangci-lint run
```

## Architecture

This is a **global opportunity ingestion and matching platform** (jobs, scholarships, funding, tenders). Eight Go microservices communicate via NATS pub/sub events, all built on the **Frame** framework (`github.com/antinvestor/frame`).

### Pipeline flow

```
Scheduler → Crawler → Worker → [Materializer, Writer, AutoApply, Matching]
```

1. **scheduler** (`apps/scheduler`) — emits `CrawlRequestedV1` events based on source freshness policies
2. **crawler** (`apps/crawler`) — runs 12+ connector types (Greenhouse, Lever, Workday, sitemaps, HTML, APIs) and emits `CrawlResultV1`
3. **worker** (`apps/worker`) — normalizes raw results into `VariantIngestedV1`, deduplicates, and publishes snapshots to Cloudflare R2
4. **materializer** (`apps/materializer`) — consumes `CanonicalsUpsertedV1` and syncs rows into the Manticore real-time index
5. **writer** (`apps/writer`) — buffers events and flushes to Iceberg/Parquet for analytics
6. **api** (`apps/api`) — REST search API backed by Manticore (`idx_opportunities_rt`)
7. **matching** (`apps/matching`) — matches candidate profiles to opportunities via LLM embeddings + reranking
8. **autoapply** (`apps/autoapply`) — end-to-end auto-application pipeline for candidates

### Event schema

All event types are in `pkg/events/v1/`. Key types: `CrawlRequestedV1`, `CrawlResultV1`, `VariantIngestedV1`, `CanonicalsUpsertedV1`, `CandidateApplicationsV1`. Events are partitioned by `partition_dt` + `partition_secondary` for Iceberg table layout.

### Key shared packages (`pkg/`)

| Package | Role |
|---|---|
| `connectors` | 12 adapter types — each returns `[]RawOpportunity` |
| `extraction` | LLM-based field extraction via OpenAI-compatible endpoints |
| `normalize` | `ExternalOpportunity` → `JobVariant` (language detection, geocoding) |
| `searchindex` | Manticore schema definition + client (`idx_opportunities_rt`) |
| `publish` | Cloudflare R2 writes + HTML snapshot generation |
| `icebergclient` | Iceberg catalog management + Parquet writers |
| `repository` | Postgres ORM (sources, candidates, flags, applications) |
| `kv` | Valkey/Redis cache for dedup snapshots and counters |
| `cv` | CV parsing, scoring, field extraction |
| `candidatestore` | Candidate profile storage |
| `domain` | Core types: source kinds, location models, opportunity categories |

### Configuration

Each app has `apps/*/config/config.go` embedding `fconfig.ConfigurationDefault` from Frame. All config is driven by environment variables (using `caarlos0/env/v11`). Copy `.env.example` to `.env` for local development.

### Database

PostgreSQL schema lives in `db/migrations/` (8 numbered SQL files, applied in order). Key tables: `sources`, `crawl_runs`, `variants`, `canonicals`, `candidates`, `applications`, `flags`. Feature flags are stored in the `flags` table, loaded at startup and cached in `pkg/repository`.

### Infrastructure (local dev)

`docker-compose.yml` (or `make infra-up`) starts:
- PostgreSQL 16 — source registry, candidates, flags
- Redis 7 — dedup locks and counters
- OpenSearch 2.14 — legacy search
- Manticore 6.3.2 (HTTP :9308, MySQL :9306) — production search index

### Definitions & seeds

- `definitions/` — YAML registries for opportunity kinds, Iceberg table schemas, Manticore index configs, trust-age policies
- `seeds/` — JSON source seeds organised by region (Africa, Asia, Europe, LATAM, MENA, Oceania)

### Integration tests

Tests under `tests/integration/` use `//go:build integration` and spin up real containers (Manticore, MinIO, NATS, Valkey, Postgres) via `testcontainers-go`. They are excluded from the default `make test` run.

### CI/CD

`.github/workflows/ci.yaml` runs unit tests + golangci-lint on PRs to main. `.github/workflows/release.yaml` builds multi-arch Docker images on `v*.*.*` tags.
