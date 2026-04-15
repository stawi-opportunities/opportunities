# 80% Global Job Coverage Ingestion Plan

## 1) System Goals and Assumptions

### Goals
- Ingest and maintain a large, fresh index of public online job postings with high global coverage.
- Reach approximately 80% of publicly accessible postings by combining:
  - 4 breadth APIs: Adzuna, SerpApi (Google Jobs), USAJOBS, SmartRecruiters Posting API
  - 8 long-tail crawlers: Greenhouse, Lever, Workday, SmartRecruiters public pages, Schema.org JobPosting, sitemap/RSS, hosted boards, generic HTML fallback
- Support 50M+ active jobs and high duplicate rates while preserving provenance.

### Non-goals
- Accessing private/paywalled postings.
- Bypassing anti-bot controls or login walls.
- Real-time guaranteed freshness for every source.

### Assumptions
- Scope: global ingest with region-level prioritization.
- Freshness target: mixed (hot sources 15-60m, normal 6-24h, cold 3-7d).
- Legal posture: robots/compliance-first, provider terms respected, official APIs preferred.
- Team starts from empty repo.

## 2) Architecture Overview

Minimal production stack (6 deployable services, can start as 2 binaries):

1. `source-discovery`
- Discovers and updates source tenants/domains and crawl type.

2. `scheduler`
- Produces crawl jobs based on freshness policy and source health.

3. `ingest-workers`
- Executes fetch -> parse -> normalize -> dedupe candidate generation.
- Pluggable connectors per API/crawler.

4. `dedupe-clusterer`
- Performs hard-key and soft-similarity clustering, canonical record selection.

5. `search-api`
- Query API over OpenSearch with filters, relevance, and canonical/variant views.

6. `ops-control-plane`
- Admin API/UI for source status, crawl policies, connector health, and replay.

Shared infrastructure:
- Postgres (system of record + metadata + canonical jobs)
- OpenSearch (search index)
- Redis (rate limits, distributed locks, short-lived queues/cache)
- Kafka or Redpanda (durable ingest events; use Redis Streams only for MVP)
- Object storage (raw fetch payload archive for replay/audit)
- OTel collector + Prometheus + Grafana + Loki/ELK

## 3) Plane Decomposition and Responsibilities

### Interaction Plane
- Public Search API
- Internal Admin API
- Webhook receiver (if a provider supports push in future)

Trust boundary:
- External clients -> API gateway -> authenticated services

### Control Plane
- Source registry
- Crawl policies (intervals, retry budgets, per-domain rate limits)
- Feature flags (connector enable/disable, region weighting)
- Dead-letter replay controls

Trust boundary:
- Operator/admin identity -> policy changes

### Execution Plane
- Connector runtime (API and crawler connectors)
- Normalization engine
- Dedupe engine
- Indexing orchestrator

Trust boundary:
- Untrusted external content -> validated normalized job records

### Data Plane
- Postgres tables (sources, crawl runs, jobs, variants, clusters)
- OpenSearch indices
- Redis keys (tokens, locks, counters)
- Raw payload object store

### Integration Plane
- External APIs: Adzuna, SerpApi, USAJOBS, SmartRecruiters
- ATS endpoints/pages: Greenhouse, Lever, Workday, others
- robots.txt + sitemap resolvers

## 4) Execution Flows (Step-by-step)

### A. Source Discovery
1. Pull SerpApi results for seeded queries by country/language.
2. Extract apply URLs and destination domains.
3. Classify source type with deterministic matchers:
   - `boards.greenhouse.io` -> greenhouse
   - `jobs.lever.co` -> lever
   - Workday patterns, SmartRecruiters patterns, schema-capable generic
4. Upsert `sources` row with confidence score and next crawl time.
5. Emit `source.discovered` event.

### B. Scheduled Crawl
1. Scheduler selects due sources by priority and health score.
2. Acquire per-source distributed lock.
3. Enqueue `crawl.requested` with idempotency key.
4. Worker executes connector with timeout budget.
5. Persist raw payload snapshot.
6. Parse and normalize into canonical model.
7. Emit `job.normalized` events.

### C. Dedupe + Canonicalization
1. Hard-key match on `(normalized_company, normalized_title, normalized_location, source_posting_id?)`.
2. If ambiguous, run soft similarity:
   - text embedding cosine threshold
   - skill/requirement Jaccard threshold
3. Assign or create `job_cluster_id`.
4. Select canonical variant by trust ranking + recency + completeness.
5. Persist variants and canonical projection.
6. Emit `job.canonical.updated`.

### D. Indexing + Serving
1. Index canonical job doc to OpenSearch.
2. Index variant metadata in secondary index (optional for explainability).
3. Search API reads canonical index; can expand duplicates on demand.

## 5) Data and State Design

### Ownership
- `source-discovery` owns `sources` classification metadata.
- `scheduler` owns `crawl_jobs` lifecycle states.
- `ingest-workers` own raw payload archive and normalized job creation.
- `dedupe-clusterer` owns cluster assignments and canonical selection.
- `search-api` owns index mappings and read-model versioning.

### Core Postgres Schema (minimal)
- `sources`
  - `id`, `source_type`, `base_url`, `country`, `status`, `crawl_interval_sec`, `health_score`, `last_seen_at`, `next_crawl_at`
- `crawl_jobs`
  - `id`, `source_id`, `scheduled_at`, `started_at`, `finished_at`, `status`, `attempt`, `idempotency_key`, `error_code`
- `raw_payloads`
  - `id`, `crawl_job_id`, `storage_uri`, `content_hash`, `fetched_at`, `http_status`
- `job_variants`
  - `id`, `external_job_id`, `source_id`, `source_url`, `apply_url`, `title`, `company`, `location_text`, `remote_type`, `employment_type`, `salary_min`, `salary_max`, `currency`, `description_text`, `posted_at`, `scraped_at`, `content_hash`
- `job_clusters`
  - `id`, `canonical_variant_id`, `confidence`, `updated_at`
- `job_cluster_members`
  - `cluster_id`, `variant_id`, `match_type` (`hard|soft`), `score`
- `canonical_jobs`
  - `id`, `cluster_id`, denormalized search fields, `is_active`, `first_seen_at`, `last_seen_at`

### OpenSearch Index
- `jobs_canonical_v1`:
  - text: title, company, description
  - keyword: country, region, source_type, employment_type, remote_type
  - numeric/date: salary range, posted_at, last_seen_at
  - vector: optional embedding for semantic retrieval

### Consistency + Recovery
- Postgres = source of truth.
- OpenSearch is rebuildable projection from `canonical_jobs`.
- Raw payload archive enables parser replay and incident backfill.

## 6) Failure and Recovery Behavior

### Retry Strategy
- Retryable: network timeout, 429, 5xx, transient DNS.
- Backoff: exponential `base=2s`, cap `5m`, full jitter.
- Attempts: 5 per crawl job, then DLQ.

### Idempotency
- `idempotency_key = sha256(source_id + scheduled_window + connector_version)`.
- Unique constraint on `crawl_jobs(idempotency_key)`.
- Upserts for variants keyed by `(source_id, external_job_id)` or `content_hash` fallback.

### Circuit Breaking
- Per-provider breaker opens on rolling error threshold (e.g., 50% failures/5m).
- Open state moves source cohort to degraded schedule.

### Degraded Modes
- API quota exhaustion: reduce query breadth, preserve high-value geos.
- OpenSearch outage: continue ingest to Postgres + backlog reindex queue.
- Postgres writer pressure: shed cold-source jobs first.

### Storage Corruption
- Validate payload hashes on read.
- Re-fetch source if corrupt and still available.
- Rebuild search index from canonical tables.

## 7) Concurrency and Scalability

### Model
- Stateless workers scale horizontally.
- Partitioning key: `source_id` for crawl; `cluster_bucket` for dedupe.
- Single-writer semantics per `source_id` enforced by lock.

### Limits (initial)
- Global concurrent crawls: 300
- Per-domain concurrent requests: 1-3
- Per-worker max in-flight jobs: 50
- Queue depth soft cap: 2M, hard cap: 5M (shed cold jobs beyond hard cap)

### Backpressure
- Priority queues: `hot`, `normal`, `cold`.
- When lag > threshold:
  - Pause cold queue ingestion.
  - Increase interval dynamically for unhealthy sources.

### Capacity Path to 50M Active Jobs
- Postgres:
  - Table partitioning by month on `job_variants.scraped_at`.
  - BRIN index for time; B-tree on source/external ids.
- OpenSearch:
  - Time + region-aware shard strategy.
  - ILM with warm tiers for stale jobs.
- Compute:
  - Autoscale workers by queue lag and p95 processing latency.

## 8) Security Model

### Authentication/Authorization
- Internal service auth via mTLS + workload identity.
- Admin API via OIDC (operator role required).
- Default-deny service-to-service policies.

### Secrets
- API keys in secret manager (not env files in git).
- Rotation every 30 days for high-risk providers, 90 days otherwise.
- Secret access audited.

### Input and Data Safety
- Treat all fetched HTML/JSON as untrusted.
- Strict parser schemas; reject unknown critical field types.
- Sanitize HTML to plain text for indexing.
- PII minimization: only job posting data, no applicant data.

### Abuse Controls
- Egress allowlist to known domains where possible.
- Domain-level rate limits.
- robots/terms compliance registry per connector.

## 9) Observability (OpenTelemetry)

### Traces
- Root span per crawl job:
  - `crawl.fetch`, `crawl.parse`, `job.normalize`, `job.dedupe`, `job.index`
- Propagate trace context through queue metadata.

### Metrics
- Counters:
  - `crawl_requests_total`, `crawl_failures_total`, `jobs_normalized_total`, `jobs_deduped_total`
- Histograms:
  - `crawl_duration_ms`, `parse_duration_ms`, `dedupe_duration_ms`, `index_duration_ms`
- Gauges:
  - `queue_depth`, `source_freshness_lag_sec`, `provider_quota_remaining`

### Logs
- JSON logs with `trace_id`, `source_id`, `crawl_job_id`.
- Redact tokens, query params containing credentials.

### SLOs (initial)
- Freshness: 95% of hot sources updated within 90 minutes.
- Availability: 99.5% successful scheduled crawl execution/day.
- Latency: p95 search query < 400ms.

## 10) Extensibility and Versioning Strategy

### Connector Interface (stable contract)
- `Discover(ctx) -> []SourceCandidate`
- `Fetch(ctx, Source) -> RawPayload`
- `Parse(ctx, RawPayload) -> []ExternalJob`
- `Normalize(ctx, ExternalJob) -> JobVariant`

Versioning:
- Connector contract is additive-only within major version.
- `connector_version` attached to each crawl job and payload.

Deprecation:
- Mark connector deprecated, dual-run old/new for 2 weeks, compare yields, then switch.

## 11) Implementation Plan (Directly Executable)

### Recommended Tech Stack
- Language: Go 1.24+
- Frameworks/libraries:
  - HTTP: `chi` or `gin`
  - DB: `pgx`, `sqlc`
  - Queue: Kafka/Redpanda client `franz-go` (Redis Streams for MVP)
  - Crawling: `colly` + custom fetcher
  - OTel: `go.opentelemetry.io/otel`
  - Search client: OpenSearch Go client

### Repository Layout
- `cmd/source-discovery`
- `cmd/scheduler`
- `cmd/worker`
- `cmd/dedupe`
- `cmd/search-api`
- `internal/connectors/{adzuna,serpapi,usajobs,smartrecruiters,greenhouse,lever,workday,schema,sitemap,hosted,generic}`
- `internal/normalize`
- `internal/dedupe`
- `internal/storage`
- `internal/telemetry`
- `deploy/{docker,k8s}`
- `docs/`

### Phase 1 (Week 1-2): MVP Coverage Core
- Build:
  - connectors: Adzuna, SerpApi, Greenhouse, Lever
  - scheduler + worker + normalization + basic hard-key dedupe
  - Postgres schema + OpenSearch indexing
- Exit criteria:
  - 1M+ ingested variants/day in staging
  - duplicate suppression > 40%
  - full replay from raw payload works

### Phase 2 (Week 3-4): Coverage Jump
- Add:
  - Schema.org crawler
  - SmartRecruiters API
  - USAJOBS API
  - soft-similarity dedupe with embeddings
- Exit criteria:
  - +25-35% unique canonical job lift over phase 1 baseline
  - precision of dedupe > 98% on labeled sample

### Phase 3 (Week 5-7): Long-tail Hardening
- Add:
  - Workday crawler
  - sitemap/RSS and hosted-board crawler
  - generic HTML fallback
  - source discovery expansion and health scoring
- Exit criteria:
  - stable crawl success > 99% for tier-1 connectors
  - hot-source freshness SLO met for 2 consecutive weeks

### Phase 4 (Week 8+): Scale and Governance
- Multi-region ingestion, cost controls, advanced ranking, compliance automation.

## 12) Simplicity and Trade-off Review

Kept intentionally simple:
- Single normalized schema for all connectors.
- Postgres as source-of-truth; OpenSearch as projection.
- Connector plugin boundary instead of separate microservice per source.

Deferred intentionally:
- Real-time stream enrichment (skills extraction, salary normalization ML).
- Complex workflow engines (Temporal/Airflow) until needed.

Trade-offs:
- API + crawl hybrid raises dedupe complexity but dramatically improves coverage.
- Workday/generic crawlers are high maintenance; isolate them behind connector contracts.

## 13) Robustness Gate (Top 3 Production Risks)

1. **Duplicate explosion from overlapping sources**
- Mitigation:
  - strict hard-key first
  - thresholded soft matching with manual eval set
  - cluster drift monitor and rollback of matcher config

2. **Provider/API blocking or quota exhaustion**
- Mitigation:
  - per-provider circuit breakers and adaptive scheduling
  - source diversity (no single provider dependency > 35% of supply)
  - cached backfill and replay windows

3. **Freshness collapse under crawl backlog growth**
- Mitigation:
  - priority queues with hard load shedding for cold sources
  - autoscaling on lag/SLO breach
  - explicit freshness budgets per source tier

---

## Immediate Next Build Tasks

1. Initialize Go monorepo with service skeletons and shared connector interfaces.
2. Create Postgres migrations for schema above.
3. Implement 4 MVP connectors: Adzuna, SerpApi, Greenhouse, Lever.
4. Implement scheduler + worker loop with idempotent crawl jobs.
5. Ship canonical indexing pipeline and search API.
6. Add OTel traces/metrics/log correlation before scaling traffic.
