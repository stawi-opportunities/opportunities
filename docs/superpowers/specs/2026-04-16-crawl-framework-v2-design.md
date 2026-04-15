# Crawl Framework V2 — Design Spec

**Date**: 2026-04-16
**Goal**: Robust, scalable job crawling framework that ingests quality global jobs into PostgreSQL. Target 1M jobs from free sources with African, European, and Australian job market coverage.

---

## 1. Constraints

- **Quality gate**: Every stored job must have title, company, location_text, description (>50 chars), and apply_url (valid URL). No exceptions.
- **Free sources only**: No paid API keys. Free JSON APIs + public board crawlers.
- **Infrastructure**: Kubernetes cluster, shared PostgreSQL and NATS, max 5 pods.
- **Framework**: Frame (`github.com/pitabwire/frame`) for all infrastructure abstractions.
- **Queue**: NATS JetStream (replaces Redis).
- **Rate limiting**: Frame's rate limiter with shared cache backend.

---

## 2. Architecture

```
┌─────────────────────┐
│  Source Registry     │  Postgres table of all known sources
│  (seed files)        │  with type, region, health, schedule, crawl_cursor
└────────┬────────────┘
         │
┌────────▼────────────┐
│  Scheduler          │  Picks due sources by priority + health_score
│  (priority-aware)   │  Publishes to NATS priority subjects
└────────┬────────────┘
         │
    ┌────▼──────┐
    │   NATS    │  Three priority subjects: hot / normal / cold
    │ JetStream │  Queue groups for load balancing
    └────┬──────┘
         │
┌────────▼────────────┐
│  Workers (3 pods)   │  Each worker: fetch → parse → quality gate
│  Pipeline stages:   │  → normalize → batch store → enqueue dedupe
│  1. Fetch raw data  │
│  2. Parse to jobs   │
│  3. Quality gate    │  REJECT if missing required fields
│  4. Normalize       │
│  5. Batch store     │  Staging table → periodic flush
│  6. Enqueue dedupe  │
└─────────────────────┘
         │
┌────────▼────────────┐
│  Dedupe (async)     │  Consumes from dedupe subject
│  Hard-key clustering│  Variants → canonical jobs
└────────┬────────────┘
         │
┌────────▼────────────┐
│  Canonical Store    │  Postgres: source of truth
│  (PostgreSQL)       │  Bulk upserts, partitioned tables
└─────────────────────┘
```

### Pod Allocation (5 pods)

| Pod | Services | Purpose |
|---|---|---|
| crawler-1, crawler-2, crawler-3 | Worker | Crawling (the bottleneck) |
| scheduler-1 | Scheduler + Discovery + Dedupe runner | Control loop |
| api-1 | Search API + Ops Control Plane | HTTP APIs |

---

## 3. NATS Queue Design

### Subjects & Streams

```
stawi.crawl.requests.hot       ← free APIs (high volume, fast)
stawi.crawl.requests.normal    ← company boards
stawi.crawl.requests.cold      ← sitemap/generic crawlers
stawi.dedupe.pending           ← workers publish variant IDs
stawi.crawl.dlq                ← dead-letter after 3 failures
```

### JetStream Configuration

- **Stream**: `CRAWL` — durable, file-backed, retention by interest
- **Consumer groups**: Workers consume via queue groups (load balanced)
- **Ack policy**: Explicit ack after full pipeline completes
- **Max redeliveries**: 3, then dead-letter to `stawi.crawl.dlq`
- **Max ack pending**: 4 per worker (matches in-flight semaphore)

### Message Schema

```json
{
  "source_id": 123,
  "source_type": "brightermonday",
  "base_url": "https://www.brightermonday.co.ke",
  "country": "KE",
  "priority": "hot",
  "scheduled_for": "2026-04-16T01:00:00Z",
  "attempt": 1,
  "idempotency_key": "sha256(...)"
}
```

---

## 4. Connector Interface

### Iterator-Based Design

```go
type Connector interface {
    Type() SourceType
    Crawl(ctx context.Context, source Source) CrawlIterator
}

type CrawlIterator interface {
    Next(ctx context.Context) bool
    Jobs() []ExternalJob
    RawPayload() []byte
    HTTPStatus() int
    Err() error
    Cursor() json.RawMessage
}
```

**Why iterator**: Paginated sources may have hundreds of pages. The iterator lets the worker process page-by-page, flushing to the database incrementally instead of holding everything in memory.

Worker loop:

```go
iter := connector.Crawl(ctx, source)
for iter.Next(ctx) {
    storeRawPayload(iter.RawPayload(), iter.HTTPStatus())
    for _, job := range iter.Jobs() {
        if err := qualityGate(job); err != nil {
            rejectJob(job, err)
            continue
        }
        variant := normalize(job)
        buffer.Add(variant)
    }
    buffer.FlushIfReady()
}
if iter.Err() != nil {
    handleError(iter.Err())
}
source.CrawlCursor = iter.Cursor()
```

---

## 5. Connector Inventory

### Tier 1 — Free JSON APIs (new, structured data)

| Connector | Endpoint | Volume | Regions |
|---|---|---|---|
| `remoteok` | `remoteok.com/api` | ~5K | Global remote |
| `arbeitnow` | `arbeitnow.com/api/job-board-api` | ~10K | EU-heavy |
| `jobicy` | `jobicy.com/api/v2/remote-jobs` | ~2K | Global remote |
| `themuse` | `themuse.com/api/public/jobs` | ~10K | US/EU |
| `himalayas` | `himalayas.app/jobs/api` | ~3K | Global remote |
| `findwork` | `findwork.dev/api/jobs` | ~5K | Global |

### Tier 2 — African Board Connectors (new, HTML parsing)

| Connector | Coverage | Volume | Strategy |
|---|---|---|---|
| `brightermonday` | Kenya, Uganda, Tanzania | ~20K+ | Listing pages → detail pages |
| `jobberman` | Nigeria, Ghana | ~30K+ | Listing pages → detail pages |
| `myjobmag` | 15+ African countries | ~50K+ | Country subdomains → listings |
| `njorku` | Pan-African (30+ countries) | ~100K+ | Aggregator listings |
| `careers24` | South Africa | ~20K+ | Listing pages → detail pages |
| `pnet` | South Africa | ~30K+ | Listing pages → detail pages |

### Tier 3 — Existing Connectors (refactored)

| Connector | Change |
|---|---|
| `greenhouse` | Refactor to iterator, seed with global company boards |
| `lever` | Refactor to iterator, seed with global company boards |
| `workday` | Refactor to iterator, already global |
| `smartrecruiters` | Keep both API and page variants, refactor to iterator |
| `smartrecruiterspage` | Refactor to iterator |
| `schemaorg` | Refactor to iterator, improve robustness |
| `sitemap` | Refactor to iterator, must follow with detail fetch for quality |
| `hostedboards` | Refactor to iterator, needs detail fetch |
| `generichtml` | Refactor to iterator, last-resort fallback |

### Removed (require paid API keys)

- `adzuna` — requires app_id + app_key
- `serpapi` — requires api_key
- `usajobs` — requires Authorization-Key

---

## 6. Source Seeding

### Seed File Structure

```
seeds/
├── apis.json              ← free API endpoints
├── africa/
│   ├── ke.json            ← Kenya: BrighterMonday, local boards
│   ├── ng.json            ← Nigeria: Jobberman, MyJobMag
│   ├── za.json            ← South Africa: Careers24, PNet
│   ├── gh.json            ← Ghana boards
│   ├── ug.json            ← Uganda boards
│   └── pan-african.json   ← Njorku, cross-border boards
├── europe/
│   ├── uk.json
│   ├── de.json
│   └── ...
├── oceania/
│   ├── au.json
│   └── nz.json
├── greenhouse_boards.json  ← curated global companies on Greenhouse
├── lever_boards.json
└── workday_sites.json
```

### Seed File Format

```json
[
  {
    "source_type": "brightermonday",
    "base_url": "https://www.brightermonday.co.ke",
    "country": "KE",
    "region": "east_africa",
    "crawl_interval_sec": 3600,
    "priority": "hot"
  }
]
```

### Discovery Service

1. **On startup**: Load all seed files, upsert into `sources` table
2. **Periodic scan** (every 6 hours): Re-read seed files for additions
3. **Dynamic discovery** (deferred): Auto-discover boards from crawled content

---

## 7. Data Model Changes

### New Columns on `job_variants`

```sql
ALTER TABLE job_variants ADD COLUMN region TEXT;
ALTER TABLE job_variants ADD COLUMN language TEXT;
ALTER TABLE job_variants ADD COLUMN source_board TEXT;
```

### New Column on `sources`

```sql
ALTER TABLE sources ADD COLUMN crawl_cursor JSONB DEFAULT '{}';
ALTER TABLE sources ADD COLUMN priority TEXT DEFAULT 'normal';
ALTER TABLE sources ADD COLUMN region TEXT;
```

### New Tables

```sql
-- Staging table for batch inserts (UNLOGGED for speed)
CREATE UNLOGGED TABLE job_variants_staging (
    LIKE job_variants INCLUDING DEFAULTS
);

-- Track page-level crawl state for incremental crawling
CREATE TABLE crawl_page_state (
    source_id    BIGINT REFERENCES sources(id),
    page_key     TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (source_id, page_key)
);

-- Track rejected jobs for connector quality monitoring
CREATE TABLE rejected_jobs (
    id BIGSERIAL PRIMARY KEY,
    source_id BIGINT REFERENCES sources(id),
    source_type TEXT NOT NULL,
    external_job_id TEXT,
    reason TEXT NOT NULL,
    raw_data JSONB,
    rejected_at TIMESTAMPTZ DEFAULT now()
);
```

### Batch Storage Flow

1. Workers batch-insert into `job_variants_staging` (UNLOGGED = no WAL = fast)
2. Periodic flush (every 30s or 500 rows) moves staging → `job_variants` via `INSERT ... ON CONFLICT DO UPDATE`
3. Dedupe runs against `job_variants` to produce `canonical_jobs`

---

## 8. Incremental Crawling

### Three Skip Mechanisms

**1. HTTP-level**: ETag / Last-Modified headers. `304 Not Modified` = skip entire page.

**2. Page-level**: Content hash comparison via `crawl_page_state` table. If hash matches, skip parsing.

**3. Job-level**: `hard_key` unique constraint on `job_variants`. Final safety net against duplicates.

### Stop-Early Heuristic

For chronologically-ordered listing crawlers: if a full page of jobs all match existing `hard_key` entries, stop paginating — we've caught up with previously-crawled content.

### Crawl Cursor

Stored as JSONB in `sources.crawl_cursor`. Connector-specific:

| Connector type | Cursor content |
|---|---|
| Paginated API | `{"last_page": 42, "last_seen_id": "abc123"}` |
| Listing crawler | `{"last_page": 15, "last_job_url": "..."}` |
| RSS feed | `{"etag": "W/xyz", "last_modified": "..."}` |
| Single-page API | `{"content_hash": "sha256..."}` |

---

## 9. Quality Gate

### Required Fields

Every job must have ALL of:

| Field | Validation |
|---|---|
| `title` | Non-empty, > 3 characters |
| `company` | Non-empty, > 1 character |
| `location_text` | Non-empty |
| `description` | Non-empty, > 50 characters |
| `apply_url` | Non-empty, valid URL (starts with http:// or https://) |

### Rejection Tracking

Rejected jobs go to `rejected_jobs` table with:
- `reason`: which field failed (e.g., "missing_title", "short_description", "invalid_apply_url")
- `raw_data`: JSONB snapshot of the ExternalJob for debugging

### Connector Quality Monitoring

If > 80% of a source's jobs get rejected in a crawl cycle, log a warning. This indicates the connector's parser needs fixing, not that the source has bad data.

---

## 10. Normalization Enhancements

On top of existing `ExternalToVariant()`:

- **Country detection**: Derive ISO country code from location text (e.g., "Nairobi" → "KE", "Lagos, Nigeria" → "NG")
- **Region assignment**: Map country to region (east_africa, west_africa, southern_africa, north_africa, europe, oceania, americas, asia)
- **Language detection**: Simple heuristic from description text (can improve later)
- **Company name normalization**: Trim "Ltd", "Inc", "Pty", "GmbH", collapse whitespace

---

## 11. Error Handling & Resilience

### Connector Failure Responses

| Failure | Detection | Response |
|---|---|---|
| Site down (5xx) | HTTP status | Retry 3x with backoff, then skip, health_score -= 0.2 |
| Rate limited (429) | HTTP status | Back off per Retry-After or exponential, pause source |
| Blocked (403/captcha) | HTTP 403 or captcha markers | Skip source, status = 'blocked', health_score = 0.0 |
| Timeout | Context deadline | Retry once, then skip. Log page progress for resume |
| Malformed response | Parse error | Log with payload sample, skip page, continue |
| Empty results | 0 jobs from previously-active source | Keep existing jobs. Log warning. |

### Health Score

```
Successful crawl:  health_score = min(1.0, health_score + 0.1)
Failed crawl:      health_score = max(0.0, health_score - 0.2)
Blocked:           health_score = 0.0, status = 'blocked'
```

Scheduler prioritizes by health_score. Degraded sources get longer intervals.

### Data Integrity

- Never delete jobs on empty crawl results
- Idempotent writes via hard_key + ON CONFLICT DO UPDATE
- Content hash detects actual job changes vs. page reformatting
- Stale job marking (is_active = false after 30 days) — deferred

### Worker Recovery

- NATS redelivers unacked messages on pod crash
- Crawl cursor only saved on completion — crash resumes from last saved position
- crawl_page_state prevents re-parsing completed pages
- Idempotent upserts handle any duplicate writes from redelivery

### Backpressure

Natural throttling via semaphore + batch buffer. When Postgres slows:
- Buffer fills, flush takes longer
- Semaphore slots stay occupied
- Fewer NATS messages consumed
- NATS accumulates durably
- System drains naturally when Postgres recovers

---

## 12. Frame Integration

| Concern | Current | Proposed |
|---|---|---|
| Database | Raw `pgxpool` | Frame database abstraction |
| Cache / Rate limiting | None | Frame rate limiter + shared cache backend |
| HTTP server | Manual `chi` setup | Frame HTTP service |
| Logging | `slog` manual | `util.Log(ctx)` |
| Telemetry | Manual OTel init | Frame built-in OTel |
| Config | Custom env loader | Frame config |
| Lifecycle | Manual signal handling | Frame service lifecycle |
| Queue | Redis `LPUSH/BRPOP` | NATS JetStream (use Frame's queue abstraction if it wraps NATS; otherwise direct `nats.go` client) |

---

## 13. Deployment

Follows the antinvestor deployments pattern (modeled on the `trustage` namespace).

### Namespace Structure in antinvestor/deployments

```
manifests/namespaces/stawi-jobs/
├── namespace.yaml                          ← namespace definition
├── kustomization.yaml                      ← root kustomization
├── kustomization_provider.yaml             ← FluxCD Kustomization CRDs per service
├── common/
│   ├── kustomization.yaml
│   ├── flux-image-automation.yaml          ← ImageRepository + ImagePolicy + ImageUpdateAutomation
│   ├── image_repository_secret.yaml        ← GHCR auth via ExternalSecret
│   ├── reference-grant.yaml                ← Gateway API ReferenceGrant
│   └── setup_queue.yaml                    ← NATS account setup
├── crawler/
│   ├── kustomization.yaml
│   ├── stawi-jobs-crawler.yaml             ← HelmRelease (colony chart) for crawler workers
│   ├── database.yaml                       ← CNPG Database + blue/green credentials
│   ├── db-credentials.yaml                 ← ExternalSecret from Vault
│   └── queue_setup.yaml                    ← NATS User + JetStream streams
├── scheduler/
│   ├── kustomization.yaml
│   ├── stawi-jobs-scheduler.yaml           ← HelmRelease for scheduler + discovery + dedupe
│   └── db-credentials.yaml                 ← shares same database, separate credentials
└── api/
    ├── kustomization.yaml
    ├── stawi-jobs-api.yaml                 ← HelmRelease for search-api + ops-control-plane
    └── db-credentials.yaml
```

### Service → Pod Mapping

| HelmRelease | cmd/ binary | Replicas | Purpose |
|---|---|---|---|
| stawi-jobs-crawler | `worker` | 3 | Crawl workers (the bottleneck) |
| stawi-jobs-scheduler | `scheduler` | 1 | Scheduler + discovery + dedupe runner |
| stawi-jobs-api | `search-api` | 1 | Search API + ops control plane |

### Colony Chart Configuration

All three services use the `colony` chart v1.10.3 from the antinvestor HelmRepository.

**Shared configuration across all services:**
- Image: `ghcr.io/stawi-jobs/stawi-jobs:<tag>` (single image, different entrypoint per service)
- Database: `pooler-rw.datastore.svc:5432/stawi_jobs` (read-write), `pooler-ro.datastore.svc:5432/stawi_jobs` (read-only)
- NATS: `nats://core-queue-headless.queue-system.svc.cluster.local:4222`
- Cache: `redis://valkey.datastore.svc:6379` (for Frame rate limiter)
- OpenTelemetry: enabled, serviceName per release
- Health probes: TCP socket on service port
- Security context: non-root, drop all capabilities, seccomp RuntimeDefault

**Crawler-specific:**
- Replicas: 3 (autoscaling disabled — fixed at 3 to stay within 5-pod budget)
- Resources: 200m CPU / 512Mi memory requests
- NATS consumer: queue group on `svc.stawi-jobs.crawl.>` subjects
- Env: `WORKER_CONCURRENCY=4` (4 per pod x 3 pods = 12 total)

**Scheduler-specific:**
- Replicas: 1 (no autoscaling — singleton)
- Resources: 50m CPU / 128Mi memory requests
- NATS publisher: publishes to `svc.stawi-jobs.crawl.>` and `svc.stawi-jobs.dedupe.>`

**API-specific:**
- Replicas: 1 (autoscaling 1-2)
- Resources: 50m CPU / 128Mi memory requests
- Gateway: enabled, HTTPRoute on configured hostnames
- CORS: enabled for web clients

### NATS JetStream Streams

```yaml
# Crawl requests stream
name: svc_stawi_jobs_crawl
subjects: ["svc.stawi-jobs.crawl.>"]
retention: workqueue
maxAge: 24h
storage: file

# Dedupe pending stream
name: svc_stawi_jobs_dedupe
subjects: ["svc.stawi-jobs.dedupe.>"]
retention: workqueue
maxAge: 24h
storage: file
```

### NATS User Permissions

```yaml
publish:
  allow: ["$JS.API.>", "svc.stawi-jobs.>"]
subscribe:
  allow: ["_INBOX.>", "svc.stawi-jobs.>"]
```

### Database (CNPG)

- Database name: `stawi_jobs`
- Owner: `stawi-jobs-crawler`
- Cluster: `hub` (shared CNPG cluster in datastore namespace)
- Extensions: `uuid-ossp`, `pg_stat_statements`, `pg_trgm`, `btree_gin`
- Blue/green credential rotation via ExternalSecrets + PushSecret to Vault
- Migration: enabled on crawler HelmRelease (runs `migrate` arg pre-install/upgrade)

### Secret Management

All secrets via Vault + ExternalSecrets:

| Secret | Vault path | Used by |
|---|---|---|
| db-credentials-stawi-jobs | `antinvestor/stawi-jobs/crawler/database` | All services |
| ghcr-auth | `antinvestor/_shared/registry/ghcr-stawi` | Image pull |
| NATS user creds | Generated by nauth.io operator | All services |

### Image Automation (FluxCD)

- ImageRepository: scans `ghcr.io/stawi-jobs/stawi-jobs`
- ImagePolicy: semver `>=v0.1.0`
- ImageUpdateAutomation: updates manifests/namespaces/stawi-jobs/ on main branch

### Project Structure Change

Align with antinvestor convention: `apps/{name}/` instead of `cmd/{name}/`. Each app has its own Dockerfile, migrations directory, and service code.

```
apps/
├── crawler/
│   ├── Dockerfile
│   ├── cmd/
│   │   └── main.go
│   └── migrations/          ← DB migrations owned by crawler
├── scheduler/
│   ├── Dockerfile
│   ├── cmd/
│   │   └── main.go
│   └── migrations/
└── api/
    ├── Dockerfile
    ├── cmd/
    │   └── main.go
    └── migrations/
pkg/                          ← shared packages (connectors, domain, etc.)
seeds/                        ← seed files baked into image
```

### Dockerfiles

Each app gets its own Dockerfile following the service-profile standard:

```dockerfile
ARG TARGETOS=linux
ARG TARGETARCH=amd64

# ---------- Builder ----------
FROM golang:1.26 AS builder

ARG TARGETOS=linux
ARG TARGETARCH=amd64

WORKDIR /app

ARG REPOSITORY
ARG VERSION=dev
ARG REVISION=none
ARG BUILDTIME

COPY go.mod go.sum ./
RUN go mod download

# Copy app-specific and shared code
COPY ./apps/crawler ./apps/crawler
COPY ./pkg ./pkg
COPY ./seeds ./seeds

# Build static binary for target platform
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
     -ldflags="-s -w \
         -X github.com/pitabwire/frame/version.Repository=${REPOSITORY} \
         -X github.com/pitabwire/frame/version.Version=${VERSION} \
         -X github.com/pitabwire/frame/version.Commit=${REVISION} \
         -X github.com/pitabwire/frame/version.Date=${BUILDTIME}" \
     -o /app/binary ./apps/crawler/cmd/main.go

# ---------- Final ----------
FROM cgr.dev/chainguard/static:latest
LABEL maintainer="Bwire Peter <bwire517@gmail.com>"

USER 65532:65532

EXPOSE 80

ARG REPOSITORY
ARG VERSION
ARG REVISION
ARG BUILDTIME
LABEL org.opencontainers.image.title="Stawi Jobs Crawler"
LABEL org.opencontainers.image.version=$VERSION
LABEL org.opencontainers.image.revision=$REVISION
LABEL org.opencontainers.image.created=$BUILDTIME
LABEL org.opencontainers.image.source=$REPOSITORY

WORKDIR /
COPY --from=builder /app/binary /crawler
COPY --from=builder /app/apps/crawler/migrations /migrations
COPY --from=builder /app/seeds /seeds

ENTRYPOINT ["/crawler"]
```

Key standards from service-profile:
- Cross-compilation support via `TARGETOS`/`TARGETARCH`
- `cgr.dev/chainguard/static:latest` base image (not distroless)
- Non-root user `65532:65532`
- Frame version injection via ldflags (`frame/version.Repository`, `.Version`, `.Commit`, `.Date`)
- `-trimpath -s -w` for reproducible, stripped binaries
- OCI metadata labels
- Migrations directory copied from builder
- `.dockerignore` excludes .git, IDE files, build artifacts, .env files, docs, tests

### .dockerignore

```
.git
.gitignore
.idea
.vscode
bin
vendor
*.exe
*.dll
*.so
*.dylib
*.test
*.out
*.log
.env
.env.local
.env.example
README.md
LICENSE
*.md
docs
```

---

## 14. Observability

### Structured Log Events

| Event | Key fields |
|---|---|
| `crawl_started` | source_id, source_type, country |
| `page_fetched` | source_id, page_key, http_status, skipped |
| `quality_rejected` | source_id, reason, external_job_id |
| `batch_flushed` | variant_count, duration_ms |
| `crawl_batch_complete` | jobs_fetched, accepted, rejected, duration |
| `dedupe_complete` | variants_processed, clusters_created |
| `schedule_tick` | sources_due, sources_scheduled |

### Health Endpoint

```json
{
  "status": "ok",
  "total_jobs": 142563,
  "total_sources": 287,
  "active_sources": 245,
  "jobs_last_hour": 12400,
  "rejection_rate": 0.08,
  "queue_depth": 34
}
```

### OpenTelemetry Traces

Per crawl request, propagated through NATS headers:

```
crawl_request (root span)
  ├── fetch_page (per page)
  ├── parse_jobs
  ├── quality_gate
  ├── normalize_batch
  ├── batch_store
  └── enqueue_dedupe
```

---

## 15. Delivery Sequence

```
1. Schema migration + seed files              ← foundation
2. Frame integration + NATS queue             ← infrastructure
3. Iterator interface + quality gate          ← pipeline core
4. Batch storage + incremental crawling       ← storage layer
5. Free JSON API connectors (6)              ← fast volume
6. African board connectors (6)              ← target audience
7. Existing connector refactor (9)           ← breadth (stretch goal)
8. Service consolidation + Dockerfile + k8s  ← deploy
9. Seed, deploy, monitor                     ← go live
```

---

## 16. Deferred (not in scope)

- Soft/semantic deduplication (embeddings, similarity matching)
- Job classification, skill extraction, category tagging
- User accounts, profiles, notifications
- Job applications on behalf of users
- Grafana dashboards
- Indeed RSS connector (complex anti-bot)
- Reed.co.uk (needs free key signup)
- Stale job cleanup (is_active = false after 30 days)
- Dynamic board discovery from crawled content
