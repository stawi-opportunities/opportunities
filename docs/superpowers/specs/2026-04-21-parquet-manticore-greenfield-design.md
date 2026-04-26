# Parquet + Manticore Greenfield — Design

Date: 2026-04-21
Status: Draft for review
Owner: Peter Bwire

## 1. Overview

Remove PostgreSQL from the hot path for both the job-seeker surfaces (search, browse, detail, matching) and the crawler pipeline (dedup, cluster, canonical merge). Postgres remains only for control-plane and non-jobs domains.

The jobs and candidate-profile data move to an **event-sourced Parquet log on R2** (source of truth), indexed live by **Manticore** (rebuildable derived view), with a **Valkey KV** for sub-millisecond dedup lookups and rerank caching. All compute pods are disposable; every stateful thing is managed infra.

This is a **greenfield** change. Existing Postgres jobs data is dropped; the platform re-crawls into the new stack. All serving and crawling pieces ship together in **v1** (full-stack, job-seeker surfaces only; recruiter-side candidate search ships in v1.1 without data migration).

## 2. Goals and non-goals

### Goals

1. **No Postgres on the hot path.** User search, browse, detail, facets, matching — none of them read from Postgres. The crawler's dedup loop — hard_key lookup on every variant — does not read from Postgres.
2. **Disposable compute.** Every pod can be killed at any time. No local state of record. Autoscales horizontally by running more identical pods.
3. **Durable event log.** Every variant, canonical, embedding, translation, CV version, and preference change lands in R2 as a Parquet file. Nothing is ever "only in memory" or "only in Manticore."
4. **AI-honest throughput.** The system never silently skips LLM calls under load. If downstream cannot keep up, the scheduler throttles crawl emission. Capacity is bought, not faked.
5. **Manticore is rebuildable.** Lose Manticore entirely and the materializer reconstructs it from R2 in hours. The crawler pipeline continues to run during any Manticore outage.
6. **Reuse existing platform services.** All time-based and workflow orchestration via Trustage; no in-process cron, no `scheduled_*` tables. Profile identity via the external Profile service; we store only `profile_id`.

### Non-goals (v1)

- Recruiter-side candidate search (`idx_candidates_rt` + free-text candidate queries). Deferred to v1.1.
- Migrating existing Postgres jobs data. Dropped; re-crawl is the migration.
- Cross-region replication of R2 Parquet. Single-region v1.
- Real-time (<10 s) serving freshness. Target is 30–60 s end-to-end.
- Postgres-less candidate identity. Profile service stays external; we keep `candidates.profile_id` in Postgres.

## 3. Decisions log

| ID | Decision | Rationale |
|---|---|---|
| **B** | Postgres only for non-jobs + control plane (sources registry, candidates identity, billing, auth, saved_jobs) | Hot-path isolation without displacing everything |
| **M2** | Parquet on R2 = source of truth; Manticore = rebuildable derived view | Aligns with agent-economy monetization (firehose + bulk dataset as products) |
| **D2** | Valkey/Redis KV for dedup + bloom filter | Sub-ms hard_key lookups; crawl path survives Manticore outages |
| **L2** | Variants + canonicals as separate Parquet partitions; variants retain `stage` | Maps 1:1 to existing pipeline handlers; full provenance |
| **W2-disposable** | Single consumer group, per-partition-key buffering, ack-after-upload, any-writer-any-partition | Zero state of record on writer pods |
| **F2** | 15–30 s writer flush, 15 s materializer poll, ~30–60 s serving freshness | Manticore strictly derivable from R2 |
| **C3 + S1** | Greenfield cut-over; full-stack v1 | Fastest path, cleanest invariants |
| **Candidates Parquet-first** | CV + improvements + preferences + embeddings → Parquet partitions; Manticore candidate index deferred | CV history becomes a product feature for free |
| **Job-seeker-only v1** | Recruiter surfaces (incl. `idx_candidates_rt`) → v1.1 | Scope discipline; no data migration later |
| **Never-drop + never-LLM-skip** | `pkg/backpressure` gate + per-stage HPAs + LLM rate limiters | Ensures quality is never silently degraded under load |
| **AI fail-open on errors, retry-forever on overload** | Handler degrades gracefully on provider error; handler holds + retries on rate limit | Preserves validator semantics, prevents silent skipping |
| **Trustage for all scheduling** | Cadenced work fires Trustage triggers that call idempotent admin endpoints | Reuses platform service; no in-process cron; no `scheduled_*` tables |
| **Profile via external service** | `candidates` row = `{id, profile_id, status, subscription, …}`; Profile service owns identity | Matches existing `CandidateProfile.ProfileID` pattern |
| **Six services, not fourteen** | `crawler`, `worker`, `writer`, `materializer`, `candidates`, `api`. Pipeline stages live as internal subscriptions inside `apps/worker`; CV lifecycle inside `apps/matching`. | Minimize ops overhead; scale inside the pod via goroutine pools, not via separate deployments. Stages remain pub/sub-decoupled so one stage's slowness doesn't block others even though they share a process. |

## 4. Architecture

### 4.1 Top-level

**Six services total — three that exist today (`crawler`, `candidates`, `api`) and three new (`worker`, `writer`, `materializer`). Each service is a binary with potentially multiple internal subscriptions; splitting further would add ops overhead without scaling benefit.**

```
                 ┌──────────────────────────────────────────┐
  Trustage ────► │ apps/crawler                             │
  (schedules)    │  - admin: scheduler tick, retention,     │
                 │    purge, reconcile, health decay        │
                 │  - consumes crawl.requests.v1            │
                 │  - fetches pages, archives to R2,        │
                 │    AI Extract + DiscoverLinks,           │
                 │    emits variants + page-completed       │
                 │  - opportunistic DiscoverSites (sampled) │
                 │  - consumes page-completed, updates      │
                 │    Postgres source cursors + health      │
                 └──────────────┬───────────────────────────┘
                                │  emits:
                                │   jobs.variants.ingested.v1
                                │   crawl.page.completed.v1 (self-consumed)
                                │   crawl.requests.v1 (continuation)
                                │   sources.discovered.v1 (sampled)
                                ▼
           ┌─────────────────────────────────────────────────┐
           │              Frame pub/sub topics               │
           └──┬───────────────────────────────────┬──────────┘
              │                                   │
              ▼                                   │
  ┌──────────────────────────────────────┐        │
  │ apps/worker                          │        │
  │   single binary, 5 internal subs:    │        │
  │                                      │        │
  │   normalize (deterministic)          │        │
  │     variants.ingested → normalized   │        │
  │                                      │        │
  │   validate (AI, fail-open)           │        │
  │     normalized → validated/flagged   │        │
  │                                      │        │
  │   dedup (KV + bloom)                 │        │
  │     validated → clustered            │        │
  │                                      │        │
  │   canonical (merge)                  │        │
  │     clustered → canonicals.upserted  │        │
  │                                      │        │
  │   embed + translate + publish        │        │
  │     (parallel consumers of           │        │
  │      canonicals.upserted)            │        │
  │     emits: jobs.embeddings,          │        │
  │            jobs.translations,        │        │
  │            jobs.published            │        │
  │                                      │        │
  │   HPA: combined input-topic lag,     │        │
  │        capped by AI backend QPS      │        │
  └──────────────────┬───────────────────┘        │
                     │                            │
                     │  emits canonical/          │
                     │  embedding/translation/    │
                     │  published events          │
                     │                            │
                     ▼                            ▼
           ┌─────────────────────────────────────────────────┐
           │              ALL event topics                   │
           └────────────────────┬────────────────────────────┘
                                ▼
                  ┌──────────────────────────────┐
                  │ apps/writer                  │
                  │  - subscribes to every topic │
                  │  - buffers per (dt, sec) key │
                  │  - flush {10k|64MB|30s}      │
                  │  - ack-after-R2-upload       │
                  │  - admin: compact.hourly,    │
                  │    compact.daily             │
                  └──────────┬───────────────────┘
                             │
                             ▼
                  ┌──────────────────────────────┐
                  │ R2: Parquet log (SoT)        │
                  │  variants/ canonicals/       │
                  │  embeddings/ translations/   │
                  │  *_current/                  │
                  │  candidates_cv/ preferences/ │
                  │  improvements/ embeddings/   │
                  │  + raw archives              │
                  └──────────┬───────────────────┘
                             │  (poll every 15s)
                             ▼
                  ┌──────────────────────────────┐
                  │ apps/materializer            │
                  │  - R2 Parquet diff scan      │
                  │  - upsert into Manticore     │
                  │  - watermark in KV           │
                  └──────────┬───────────────────┘
                             ▼
                  ┌──────────────────────────────┐
                  │ Manticore idx_opportunities_rt        │
                  │  FTS + attr + HNSW vector    │
                  └──────────┬───────────────────┘
                             │
   ┌─────────────────────────┴─────────────────────────┐
   │                                                   │
   ▼                                                   ▼
┌──────────────────────────┐              ┌─────────────────────────────┐
│ apps/api                 │              │ apps/matching             │
│  - job search / browse / │              │  - CV upload HTTP endpoint  │
│    detail / saved jobs / │              │  - consumes candidates.cv.* │
│    "jobs for me" matches │◄─────────────│    (extract, score,         │
│  - Manticore reads       │  match reads │    improve, embed)          │
│  - synchronous rerank    │              │  - emits candidates.cv.*    │
│    (paid tier, KV cache) │              │    candidates.preferences.* │
│                          │              │    candidates.embeddings.*  │
│                          │              │  - admin: matches.weekly,   │
│                          │              │    cv.stale_nudge           │
└──────────────────────────┘              └─────────────────────────────┘


  Valkey/Redis KV                Postgres (control plane + non-jobs)
   - dedup:{hard_key}             - sources (+ cursors, health, quality)
   - cluster:{cluster_id}         - candidates (id, profile_id, status, sub)
   - rerank:{mv:qh:fh} (TTL)      - subscriptions / entitlements
   - bloom:dedup:{shard}          - saved_jobs
   - match_rate:{cand_id}         - crawl_jobs (optional, ops history)
   - mat:watermark:{part}         - billing tables

  Profile service (external)       Trustage (external)
   owns identity, auth,             fires admin endpoints on cadence;
   email, MFA                       no in-process cron anywhere
```

### 4.2 Service inventory

**Six services. Each is a single binary with potentially multiple pub/sub subscriptions and/or admin HTTP endpoints inside one process. Splitting further would add ops overhead (deployment, monitoring, alerting) without scaling benefit.** All services are disposable (no local state of record) and autoscale on appropriate lag/load signals.

| Service | Exists today? | Responsibilities | Inputs | Outputs | AI? |
|---|---|---|---|---|---|
| `apps/crawler` | yes (extended) | Fetch pages, archive raw bytes to R2, AI extract (`Extract`, `DiscoverLinks`, opportunistic `DiscoverSites` via sampling); consume `crawl.requests.v1`; self-consume `crawl.page.completed.v1` to update `sources` (cursor, health, quality); Trustage admin endpoints (scheduler tick, retention, purge, reconcile, health decay) | `crawl.requests.v1`, `crawl.page.completed.v1` | `jobs.variants.ingested.v1`, `crawl.page.completed.v1`, continuation `crawl.requests.v1`, `sources.discovered.v1` | chat (Extract, DiscoverLinks, DiscoverSites) |
| `apps/worker` | **new** | Single binary running all pipeline stages as independent internal subscriptions: normalize (deterministic) → validate (AI, fail-open) → dedup (KV + bloom) → canonical merge → (embed, translate, publish — parallel consumers of `canonicals.upserted`). Per-stage goroutine pools with per-backend rate limiters. | `jobs.variants.ingested.v1`, `jobs.variants.normalized.v1`, `jobs.variants.validated.v1`, `jobs.variants.clustered.v1`, `jobs.canonicals.upserted.v1` | `jobs.variants.normalized.v1`, `jobs.variants.validated.v1` or `.flagged.v1`, `jobs.variants.clustered.v1`, `jobs.canonicals.upserted.v1`, `jobs.embeddings.v1`, `jobs.translations.v1`, `jobs.published.v1` (+ R2 publish write) | chat (validate, translate), embed (job embeddings) |
| `apps/writer` | **new** | Subscribes to every event topic. Buffers in-memory keyed by `(partition_dt, partition_sec)`. Flushes to R2 Parquet on `{10k events | 64 MB | 30 s}`. Acks only after R2 ETag confirms. Trustage admin endpoints for compaction. | all `*.v1` topics | R2 Parquet files | no |
| `apps/materializer` | **new** | Polls R2 every 15 s for new Parquet files in tracked partitions. Upserts to Manticore `idx_opportunities_rt`. Watermark in KV. | R2 Parquet diffs | Manticore upserts | no |
| `apps/matching` | yes (extended) | CV upload HTTP endpoint (synchronous extract + archive), consume candidates.cv.* topics internally for improve/embed/score, match endpoint for "jobs for me", Trustage admin endpoints (`matches.weekly_digest`, `cv.stale_nudge`). One binary owning the full candidate lifecycle. | HTTP (upload, match), `candidates.cv.uploaded.v1`, `candidates.cv.extracted.v1`, `candidates.cv.improved.v1` | `candidates.cv.uploaded.v1`, `candidates.cv.extracted.v1`, `candidates.cv.improved.v1`, `candidates.preferences.updated.v1`, `candidates.embeddings.v1`, `candidates.matches.ready.v1` | chat (ExtractCV, rewrites), embed (CV embeddings), scorer |
| `apps/api` | yes (modified) | Job-seeker HTTP: search, browse, detail, facets, saved jobs, read-my-matches. All reads from Manticore + KV. Synchronous rerank on paid-tier requests. | HTTP | Manticore queries, KV reads, rerank calls | rerank (paid tier only, KV-cached) |

**Why consolidate into `apps/worker` instead of 7 separate services:**

- All stages operate on the same variant/canonical lifecycle and share the same dependencies (Extractor, KV client, Frame pub/sub client, metric labels, bloom filter handle).
- Each stage is independently scaled *within* the pod via goroutine pool size + per-backend rate limiter, not by separate HPAs.
- The HPA scales the single `apps/worker` pod on combined pub/sub lag across its subscriptions. If validate is the bottleneck, its input topic lag grows, HPA kicks up more pods; other stages in the same pod get idle capacity at no extra cost.
- Ops overhead: 1 deployment, 1 HPA config, 1 dashboard, 1 alert group instead of 7.
- Stages remain decoupled via pub/sub topics (each stage has its own consumer group), so a validator slowdown never directly blocks normalize — they're separate consumers inside one process.
- **`apps/worker`, `apps/writer`, and `apps/materializer` are kept separate** because they have materially different workloads (CPU + AI vs IO + buffer-heavy vs polling + index-writing) and different scaling signals. Merging them would conflate orthogonal concerns.

**Trustage-fired admin endpoints** (no long-running cron, no `scheduled_*` tables):

| Trigger | Cadence | Pod (admin endpoint) | Work |
|---|---|---|---|
| `scheduler.tick` | every 30 s | `apps/crawler` | `SourceRepo.ListDue` → `backpressure.Gate.Admit` → emit `crawl.requests.v1` |
| `compact.hourly` | hourly | `apps/writer` | merge small Parquet files in today's partitions |
| `compact.daily` | daily 02:00 UTC | `apps/writer` | rebuild `*_current/` partitions from the day's log |
| `retention.expire` | every 15 min | `apps/crawler` (existing) | flip canonicals past `expires_at` — emits `jobs.canonicals.expired.v1` |
| `retention.purge_r2` | nightly | `apps/crawler` (existing) | purge R2 publish snapshots past grace window |
| `retention.reconcile` | nightly | `apps/crawler` (existing) | reconcile R2 archive vs event log |
| `sources.quality_window_reset` | weekly | `apps/crawler` | reset quality counters |
| `sources.health_decay` | hourly | `apps/crawler` | gentle decay of `health_score` toward 1.0 |
| `matches.weekly_digest` | weekly per-candidate (Trustage per-entity schedule) | `apps/matching` | run matching pipeline, emit `candidates.matches.ready.v1` for delivery service |
| `cv.stale_nudge` | Trustage follow-up after N days idle | `apps/matching` | emit nudge event for notification service |

Trigger definitions live in `definitions/trustage/*.json` alongside the existing `retention-*.json`. Every admin endpoint is idempotent.

### 4.3 Managed infrastructure

| Component | Role |
|---|---|
| Cloudflare R2 | Parquet log (source of truth) + raw page archive + publish snapshots |
| Valkey / Redis | dedup KV, bloom filters, rerank cache |
| Manticore Search | FTS + attribute filtering + HNSW vector search (`idx_opportunities_rt` in v1) |
| Frame pub/sub | event transport, durable, at-least-once |
| PostgreSQL | control-plane (sources), identity ref (candidates), billing, auth, saved_jobs |
| Profile service (external) | candidate identity, auth, email, MFA |
| Trustage (external) | scheduled triggers, follow-up workflows |
| TEI chat / TEI embed / TEI rerank | AI inference backends (each independently scaled) |

## 5. Data model

### 5.1 Event envelope

All pub/sub payloads share an envelope, serialized as Protobuf (`pkg/events/proto/*.proto`, lint/breakage checked with `buf`):

```protobuf
message EventEnvelope {
  string      event_id       = 1;  // xid, globally unique
  string      event_type     = 2;  // e.g. "jobs.variants.ingested.v1"
  int64       occurred_at_ns = 3;
  string      partition_dt   = 4;  // "YYYY-MM-DD" (UTC)
  string      partition_sec  = 5;  // secondary key (source_id, cluster_prefix, lang, cnd_prefix)
  uint32      schema_version = 6;  // within event_type; bump for additive fields
  string      trace_id       = 7;  // OTEL propagation
  bytes       payload        = 8;  // serialized payload message
}
```

Breaking changes bump the `.vN` suffix on `event_type` — producers emit the new version, consumers subscribe to both during rollout.

### 5.2 Parquet partition layout

All partitions live under a single R2 bucket, e.g. `r2://opportunities-log/`.

**Jobs partitions:**

| Path | Columns (abbreviated) | Compaction |
|---|---|---|
| `variants/dt=YYYY-MM-DD/src=<source_id>/<uuid>.parquet` | variant_id, source_id, external_job_id, hard_key, stage, title, company, location_text, country, language, remote_type, employment_type, salary_min/max, currency, description (≤500 words), apply_url, posted_at, scraped_at, content_hash, raw_archive_ref, extended JobFields (urgency_level, funnel_complexity, company_size, funding_stage, required_skills[], nice_to_have_skills[], tools_frameworks[], geo_restrictions, timezone_req, application_type, ats_platform, role_scope, team_size, reports_to), validation_score, validation_notes, validation_recommendation, model_version_extract, model_version_validate, occurred_at | hourly within day; daily roll-up |
| `canonicals/dt=YYYY-MM-DD/<uuid>.parquet` | canonical_id, cluster_id, slug, title, company, description, country, language, remote_type, employment_type, salary_*, seniority, skills[], roles[], required_skills[], benefits[], category, quality_score, posted_at, first_seen_at, last_seen_at, expires_at, status, redirect_link_id, redirect_slug, translated_langs[], published_at, merge_source_variant_ids[], model_versions{}, occurred_at | hourly |
| `canonicals_current/cc=<cluster_id_prefix_2hex>/<file>.parquet` | Latest canonical per cluster_id, rebuilt daily by compactor | daily rebuild |
| `embeddings/dt=/<uuid>.parquet` | canonical_id, vector float[DIM], model_version, occurred_at | hourly |
| `embeddings_current/cc=<cluster_id_prefix_2hex>/<file>.parquet` | Latest embedding per canonical_id | daily rebuild |
| `translations/dt=/lang=/<uuid>.parquet` | canonical_id, lang, title_tr, description_tr, model_version, occurred_at | daily |
| `translations_current/cc=/lang=/<file>.parquet` | Latest per (canonical_id, lang) | daily rebuild |
| `match_decisions/dt=/<uuid>.parquet` | candidate_id, canonical_id, stage2_score, stage3_score, rerank_model_version, decided_at | none (audit) |
| `jobs_expired/dt=/<uuid>.parquet` | canonical_id, expired_at | none (audit) |
| `jobs_published/dt=/<uuid>.parquet` | canonical_id, slug, r2_version, published_at | none (audit) |

**Candidates partitions:**

| Path | Columns | Compaction |
|---|---|---|
| `candidates_cv/dt=/cnd=<cnd_prefix_2hex>/<uuid>.parquet` | candidate_id, cv_version (int), raw_archive_ref (R2 pointer to PDF/DOCX), CVFields (name, email, phone, location, current_title, bio, seniority, years_experience, primary_industry, strong_skills[], working_skills[], tools_frameworks[], certifications[], preferred_roles[], languages[], education, work_history[], preferred_locations[], remote_preference, salary_min/max, currency), score_components (ats, keywords, impact, role_fit, clarity, overall), model_versions{extract, score}, occurred_at | hourly |
| `candidates_cv_current/cnd=<cnd_prefix_2hex>/<file>.parquet` | Latest CV version per candidate_id | daily rebuild |
| `candidates_improvements/dt=/cnd=<cnd_prefix_2hex>/<uuid>.parquet` | candidate_id, cv_version, fix_batch[] ({fix_id, title, impact, category, why, auto_applicable, rewrite}), model_version, occurred_at | hourly |
| `candidates_preferences/dt=/<uuid>.parquet` | candidate_id, remote_preference, salary_min/max, currency, preferred_locations[], excluded_companies[], target_roles[], languages[], availability, occurred_at | daily |
| `candidates_preferences_current/cnd=<cnd_prefix_2hex>/<file>.parquet` | Latest preferences per candidate_id | daily rebuild |
| `candidates_embeddings/dt=/<uuid>.parquet` | candidate_id, cv_version, vector float[DIM], model_version, occurred_at | daily |
| `candidates_embeddings_current/cnd=<cnd_prefix_2hex>/<file>.parquet` | Latest embedding per candidate_id | daily rebuild |
| `candidates_matches_ready/dt=/<uuid>.parquet` | candidate_id, match_batch_id, matches[] ({canonical_id, match_score, rerank_score?}), occurred_at | weekly |

### 5.3 Manticore — `idx_opportunities_rt` (v1)

```sql
CREATE TABLE idx_opportunities_rt (
  id                bigint PRIMARY KEY,
  canonical_id      string attribute indexed,
  slug              string attribute,

  -- full-text fields (weights A/B/C/D as in current Postgres)
  title             text indexed,           -- weight A
  company           text indexed,           -- weight B
  skills            text indexed,           -- weight C
  description       text indexed stored,    -- weight D, stored for snippets

  roles             text indexed,
  location_text     text indexed,

  -- filter / facet attributes
  category          string attribute,
  country           string attribute,
  language          string attribute,
  remote_type       string attribute,
  employment_type   string attribute,
  seniority         string attribute,
  industry          string attribute,
  ats_platform      string attribute,
  salary_min        uint,
  salary_max        uint,
  currency          string attribute,
  quality_score     float,
  is_featured       bool,                    -- derived: quality_score >= 80
  posted_at         timestamp,
  last_seen_at      timestamp,
  expires_at        timestamp,
  status            string attribute,        -- 'active' only in serving index

  -- vector (hybrid search)
  embedding         float_vector[DIM] knn_type='hnsw' hnsw_similarity='cosine',
  embedding_model   string attribute,

  -- translation readiness
  translated_langs  multi string
);
```

- FTS weights match the current Postgres tsvector weighting (title A, company B, skills C, description D).
- Facets served natively: `FACET category, country, remote_type, employment_type, seniority` — no `mv_job_facets` table.
- API always appends `WHERE status='active' AND expires_at > now()`.
- Hybrid search: `MATCH('…') OPTION … | KNN(embedding, query_vec, 200)` then merge + rerank at API layer.

### 5.4 KV (Valkey) schema

| Key pattern | Value | TTL | Rebuildable from |
|---|---|---|---|
| `dedup:{hard_key}` | cluster_id (20-byte xid) | none | `canonicals_current/` + `variants/` |
| `cluster:{cluster_id}` | msgpack'd compact canonical snapshot (fields needed for merge) | none, evicted on retire | `canonicals_current/` |
| `rerank:{model_v}:{query_hash}:{filter_hash}` | `[canonical_id, …]` | 24h | cache-only; regenerated by reranker |
| `bloom:dedup:{shard_hex}` | counting bloom bits for hard_keys | none | `canonicals_current/` |
| `match_rate:{candidate_id}` | rate-limit counter for "my matches" | 1h | ephemeral |
| `cv_ratelimit:{profile_id}` | rate-limit for CV upload frequency | 1h | ephemeral |

All KV keys are derived state. Cold start procedure (§9.4) rebuilds `dedup:*`, `cluster:*`, `bloom:dedup:*` from `canonicals_current/`.

### 5.5 Postgres residue

**Tables that stay:**

| Table | Columns (summary) | Why |
|---|---|---|
| `sources` | id, type, base_url, name, country, language, status, priority, crawl_interval_sec, next_crawl_at, last_seen_at, health_score, consecutive_failures, config (opaque cursor), quality_window_*, needs_tuning | Control plane — Trustage-fired scheduler reads `ListDue`; not on user hot path |
| `candidates` | id, profile_id, status, subscription, created_at, updated_at | Identity ref only; Profile service owns auth/email/PII |
| `subscriptions`, `entitlements` | billing state | `pkg/billing` owns this |
| `saved_jobs` | candidate_id, canonical_id, saved_at | Small FK table; fine on Postgres |
| `crawl_jobs` (optional) | run history | Ops audit only |

**Tables dropped in greenfield:**

`canonical_jobs`, `job_variants`, `job_clusters`, `job_cluster_members`, `crawl_page_state`, `rerank_cache`, `mv_job_facets`, and the CV/preferences/embedding columns on `candidates` (migrated to Parquet partitions).

## 6. Data flows

### 6.1 Crawl a new job (happy path)

1. Trustage fires `scheduler.tick` (every 30 s) → `POST /_admin/scheduler/tick` on `apps/crawler`.
2. Scheduler queries `SourceRepo.ListDue(now, limit)` → N due sources.
3. Scheduler consults `backpressure.Gate.Admit("crawl.requests.v1", N)` → granted K ≤ N. For the K admitted sources, emit one `crawl.requests.v1` event each; stamp `next_crawl_at` forward. For the (N-K) deferred, leave `next_crawl_at` untouched (next tick tries again).
4. `apps/crawler` consumes a `crawl.requests.v1`:
   1. Resolve connector from `connectors.Registry`.
   2. Fetch the page; archive raw bytes to R2 via `pkg/archive`, get `archive_ref`.
   3. If HTML listing: call `extractor.DiscoverLinks(ctx, markdown, pageURL)` → child job URLs → emit one `crawl.requests.v1` per detail URL (chained). Emit a follow-up `crawl.requests.v1` with the next listing cursor if present.
   4. If detail: call `extractor.Extract(ctx, rawHTML, pageURL)` → `JobFields`. Emit `jobs.variants.ingested.v1` with fields + `archive_ref` + `model_version_extract`.
   5. Emit `crawl.page.completed.v1` (source_id, cursor, http_status, job_count).
5. Inside `apps/worker` — the **normalize** subscription consumes `jobs.variants.ingested.v1` → runs deterministic normalization (country codes, salary parsing, remote-type inference) → emits `jobs.variants.normalized.v1`.
6. Inside `apps/worker` — the **validate** subscription consumes `jobs.variants.normalized.v1` → calls `extractor.Prompt(validationPrompt, reviewInput)` → parses `{valid, confidence, issues, recommendation}`:
   - If `valid && confidence >= 0.7`: emit `jobs.variants.validated.v1`.
   - Else: emit `jobs.variants.flagged.v1` (audit sink).
   - On LLM *error* (network, 5xx): **fail-open** — emit `validated` with `validation_score=0.5`, `validation_notes="LLM unavailable: …"`. (On LLM *rate-limit / 429*: hold, retry with backoff. Do not fail-open for overload.)
7. Inside `apps/worker` — the **dedup** subscription consumes `jobs.variants.validated.v1`:
   1. Compute `hard_key` (deterministic from source_id + external_job_id).
   2. Check bloom `bloom:dedup:{shard}` — if absent, this is new; `KV SET dedup:{hard_key} := new_cluster_id`; update bloom; emit `jobs.variants.clustered.v1` with `cluster_id`.
   3. If present, `KV GET dedup:{hard_key}` → `cluster_id`; emit `jobs.variants.clustered.v1`.
   4. (Soft clustering — title/company/location heuristic — runs against `KV cluster:{prefix}:*` secondary lookup; falls back to Manticore if KV miss; degraded to hard-key-only if both unavailable.)
8. Inside `apps/worker` — the **canonical** subscription consumes `jobs.variants.clustered.v1`:
   1. `KV GET cluster:{cluster_id}` → existing snapshot (or nil).
   2. Merge the new variant fields against the snapshot (preferring higher-quality fields).
   3. `KV SET cluster:{cluster_id} := new_snapshot`.
   4. Emit `jobs.canonicals.upserted.v1` with the merged canonical.
9. Inside `apps/worker` — three parallel subscriptions on `jobs.canonicals.upserted.v1` (each its own consumer group so one's slowness doesn't block the others):
   - **embed** → calls `extractor.Embed(ctx, text)` → emits `jobs.embeddings.v1`.
   - **translate** → for each configured target language, calls `extractor.Prompt(translatePrompt, …)` → emits `jobs.translations.v1` per lang.
   - **publish** → writes R2 job snapshot, emits `jobs.published.v1`.
10. `apps/writer` subscribes to **all** event topics. For each consumed event, it buffers in memory keyed by `(partition_dt, partition_sec)`, and flushes a Parquet file to R2 when any buffer hits 10k events / 64 MB / 30 s. Ack to pub/sub only after R2 upload confirms.
11. `apps/materializer` polls R2 every 15 s for new files under all partitions it tracks. For each new file: read rows, upsert into `idx_opportunities_rt` (RT index UPDATE/INSERT). Stores a per-partition watermark in KV (`mat:watermark:{partition}`) so restarts resume cleanly.
12. Search is now live: the user's next query via `apps/api` hits Manticore, which has the canonical + embedding + translation state.

End-to-end latency from crawl to searchable: dominated by writer flush (≤30 s) + materializer poll (≤15 s) + Manticore RT index apply (<1 s) → **typically 30–60 s**.

### 6.2 CV upload → matching

All candidate-side work lives in `apps/matching` — one binary, multiple internal subscriptions, same pattern as `apps/worker`.

1. User uploads CV via HTTP → `apps/matching` upload endpoint (synchronous for the user).
2. Upload handler archives the file to R2 → gets `raw_archive_ref`. Extracts text via `ExtractTextFromPDF/DOCX`. Emits `candidates.cv.uploaded.v1`.
3. Inside `apps/matching` — the **cv-extract** subscription consumes `candidates.cv.uploaded.v1` → calls `extractor.ExtractCV(ctx, text)` → `CVFields`. Runs `cv.Scorer.Score` (role-fit component uses `extractor.Embed` internally). Emits `candidates.cv.extracted.v1` with `CVFields` + `score_components`.
4. Inside `apps/matching` — the **cv-improve** subscription consumes `candidates.cv.extracted.v1` → runs deterministic `fixes.detectPriorityFixes` + LLM `AttachRewrites`. Emits `candidates.cv.improved.v1`.
5. Inside `apps/matching` — the **cv-embed** subscription consumes both `candidates.cv.extracted.v1` and `candidates.cv.improved.v1` → calls `extractor.Embed(ctx, cv_text)` → emits `candidates.embeddings.v1`.
6. `apps/writer` persists all four event types to their respective Parquet partitions. `apps/materializer` at v1 does *not* index candidate data into Manticore (deferred to v1.1).
7. When the user (or Trustage `matches.weekly_digest`) requests matches, `apps/matching` match endpoint:
   1. Reads the candidate's embedding + preferences from R2 `candidates_*_current/` partitions directly (or via a small KV cache).
   2. Queries `idx_opportunities_rt` with hard filters (remote_preference, salary floor, preferred_locations) + KNN on candidate embedding → top 200.
   3. Runs in-Go composite scoring (skills overlap, salary fit, recency, seniority) → top K.
   4. If reranker enabled: `KV GET rerank:{mv}:{cv_hash}:{filter_hash}` → hit returns cached order; miss calls `extractor.Rerank` (200 ms timeout), KV SETs result (TTL 24 h). Fail-open on error/timeout (return composite order).
   5. Emits `candidates.matches.ready.v1` → writer → `candidates_matches_ready/` partition.
   6. Trustage-driven delivery consumer picks up the event and sends notification/email.

### 6.3 User search (free tier)

1. `GET /search?q=react&country=KE&remote_type=remote&limit=20` hits `apps/api`.
2. API constructs a Manticore query:
   ```sql
   SELECT id, slug, title, company, …, snippet(description, …)
   FROM idx_opportunities_rt
   WHERE MATCH('react')
     AND status='active'
     AND expires_at > UNIX_TIMESTAMP()
     AND country='KE'
     AND remote_type='remote'
   ORDER BY WEIGHT() DESC, posted_at DESC
   LIMIT 21
   OPTION ranker=proximity_bm25
   FACET category, remote_type, seniority, employment_type, country
   ```
3. Returns results + facets in one round-trip. No Postgres. No rerank (free tier).
4. `is_featured` is a denormalized column on Manticore (`quality_score >= 80`) — no join needed.

### 6.4 User search (paid tier, with rerank)

As above, plus:

- Top 200 results from Manticore are passed through the reranker (`apps/api` inline call).
- KV cache key: `rerank:{model_v}:{sha256(query ∥ filter_json)}:{tier}`. Hit returns cached order directly; miss calls `extractor.Rerank`.
- Timeout 200 ms; on timeout/error, return un-reranked order. API returns `x-rerank-applied: false` header so clients know the quality tier they got.

### 6.5 Backpressure activation

Normal: every topic's `estimated_drain_time` is under its ceiling; gate admits 100% of scheduler requests.

Mild pressure: one stage (e.g., `jobs.variants.normalized.v1`) lags because an LLM backend is slow. HPA on `apps/worker` scales pods up to the backend rate-limit cap; inside each pod the validate subscription's goroutine pool runs at its own limiter while other stages run unimpeded. Scheduler gate still admits full.

Heavy pressure: validator is already at HPA ceiling, backend is rate-limited, queue depth grows. Gate's computation of `estimated_drain_time > 15 min` triggers admission throttle. Scheduler admits 60% of due sources per tick; deferred sources wait for the next tick. No events are dropped; operators see a sustained "saturation score" on the ops dashboard and either (a) add inference capacity, or (b) live with a slower crawl for a few hours.

Extreme: multiple stages saturated simultaneously. Gate emits `0` admissions; scheduler emits nothing for this tick. Crawlers drain existing `crawl.requests.v1`; downstream catches up; gate lifts the throttle when drain times recover. The pipeline is always consistent; just slower.

### 6.6 Manticore outage

Materializer cannot reach Manticore. It logs errors, backs off, and stops advancing its watermark. Writer continues producing Parquet files to R2. Crawler pipeline continues; dedup hits KV only. Search API returns 503 with `Retry-After`.

When Manticore returns: materializer resumes from its watermark and replays missed files. No data is lost.

Full Manticore rebuild from scratch: start materializer with watermark = epoch 0; it replays `canonicals_current/ + embeddings_current/ + translations_current/` in partition order; fresh HNSW index builds as rows flow in. At current volume (<10 M canonicals), hours to a day.

### 6.7 Writer outage

Writer pods down. Pub/sub backlog grows (all topics). HPA notices topic lag on writer's consumer group → starts more writer pods (or the existing pods come back). On boot, writer consumers resume from last ack; unacked events redeliver; duplicates in Parquet will be resolved by the next compaction pass (dedup by `event_id`).

### 6.8 KV outage

KV down. Crawler dedup cannot do sub-ms lookups. Dedup handler holds messages (not-acked) and retries with backoff; queue lag grows; scheduler gate throttles; effective crawl rate drops to KV-recovery rate. When KV returns: dedup proceeds; if the KV was rebuilt from scratch (lost replica), the first lookup per hard_key misses → dedup treats as new variant → possibly creates duplicate cluster → next compaction re-merges on a matching hard_key collision. (Duplicate cluster detection is part of the hourly compaction pass.)

## 7. AI integration

### 7.1 Inventory

Single shared abstraction `extraction.Extractor` with three independently-configured backends:

- `chat` → OpenAI-compatible `/v1/chat/completions` (used by: `Extract`, `ExtractCV`, `DiscoverLinks`, `DiscoverSites`, `Prompt`)
- `embed` → OpenAI-compatible `/v1/embeddings` (used by: `Embed`)
- `rerank` → HuggingFace TEI-compatible `/rerank` (used by: `Rerank`)

Each resolved independently via `ResolveInference / ResolveEmbedding / ResolveRerank` with provider-specific fallback chains. Any one can be disabled (empty BaseURL) — the code already degrades gracefully.

### 7.2 Call-site catalog

| Site | Service | Purpose | Synchronous? | Fail-mode |
|---|---|---|---|---|
| `DiscoverLinks` | `apps/crawler` | Listing HTML → detail URLs | Yes, per listing page | sitemap-first fallback (already in `universal.Connector`) |
| `Extract` | `apps/crawler` | Detail HTML → `JobFields` | Yes | drop variant |
| `DiscoverSites` | `apps/crawler` (sampled) | Page → new job boards | Async | drop |
| `Prompt(validationPrompt)` | `apps/worker` (validate sub) | Variant → accept/flag | Async | **fail-open @ 0.5 on error**; retry on overload |
| `Prompt(translatePrompt)` | `apps/worker` (translate sub) | Canonical → target lang | Async, per (job, lang) | skip language |
| `Embed` (jobs) | `apps/worker` (embed sub) | Canonical → vector | Async | skip vector (search degrades to BM25) |
| `ExtractCV` | `apps/matching` | CV text → `CVFields` | Sync (user-facing upload) | error to user |
| `Scorer.Score` (role-fit uses `Embed`) | `apps/matching` | CV score component | Sync | degrade role-fit to neutral 60 |
| `Prompt(rewritePrompt)` | `apps/matching` (cv-improve sub) | CV bullet rewrites | Async | skip rewrites, keep deterministic fixes |
| `Embed` (CV) | `apps/matching` (cv-embed sub) | CV text → vector | Async | skip vector |
| `Rerank` (API) | `apps/api` | Top-200 job reorder | Sync (paid tier) | un-reranked fallback |
| `Rerank` (matches) | `apps/matching` | Stage 3 per-match reorder | Sync (per match run) | retrieval-order fallback |

### 7.3 Model versioning

Every event payload that derives from an AI call carries `model_version` fields. Parquet rows preserve these. Consequences:

- `canonicals.upserted.v1` includes `model_version_extract` (from the variant) + `model_version_validate`.
- `jobs.embeddings.v1` and `candidates.embeddings.v1` include `model_version`.
- `jobs.translations.v1` includes `model_version` per lang.
- Rerank cache key includes `model_version` so a model swap naturally invalidates the cache.
- Re-embedding with a new model = bump the embedder config + emit replay job that reads `canonicals_current/` and emits fresh `jobs.embeddings.v1` events. Materializer writes the new vectors; Manticore prefers newest by `embedding_model` attribute (or we replace by canonical_id + model filter).

### 7.4 Rate limits and never-drop

- Each `Extractor` call is metered per-backend by an in-client token bucket. Configured from `INFERENCE_RATE_QPS / EMBEDDING_RATE_QPS / RERANK_RATE_QPS` env vars.
- Handlers holding on rate limit do **not** ack the event. Pub/sub redelivery handles retries with exponential backoff (Frame config).
- There is **no** "skip LLM on rate limit" branch anywhere. Rate limit means "slower," not "lossy."
- Fail-open applies only to real *errors* (provider outage, 5xx). It does not apply to 429s; those retry.

### 7.5 HPA caps tied to AI capacity

Each service has one HPA. `apps/worker` scales on its aggregate input-topic lag; the cap is derived from the *most constrained* backend the worker uses at peak:

```
# apps/worker — one HPA, cap chosen from its busiest AI consumer
max_replicas(worker) = floor(
  min(
    backend.chat_rate_qps  / per_pod_chat_qps_at_peak,   // validate + translate
    backend.embed_rate_qps / per_pod_embed_qps_at_peak,  // embed
  )
)

# apps/matching — one HPA, cap from its busiest AI consumer
max_replicas(candidates) = floor(
  min(
    backend.chat_rate_qps  / per_pod_cv_chat_qps,        // ExtractCV + improver
    backend.embed_rate_qps / per_pod_cv_embed_qps,       // CV embed
  )
)

# apps/crawler — cap from chat QPS (Extract + DiscoverLinks)
max_replicas(crawler) = floor(backend.chat_rate_qps / per_pod_crawl_chat_qps)
```

Scaling past these caps only increases queue contention; it doesn't buy throughput. The gate throttles upstream instead. Inside each pod, per-stage goroutine pools are sized so the busiest stage can't starve the others of backend QPS budget — each subscription has its own token bucket against the shared backend limiter.

## 8. Backpressure design

### 8.1 Signals

Every pub/sub topic exports to Prometheus (via Frame's pub/sub adapter + OTEL):

- `queue_depth{topic, group}`
- `queue_consume_rate{topic, group}`
- Derived: `estimated_drain_time = depth / rate`

Every LLM backend exports:

- `llm_inflight{backend}`
- `llm_p95_latency_ms{backend}`
- `llm_rate_limit_hits{backend}`

Every handler exports (existing `pkg/telemetry`):

- `stage_duration_seconds{stage}`
- `stage_transitions_total{from, to}`

### 8.2 Three layers

Ordered by which kicks in first:

1. **LLM client rate limiter (in-handler).** Token bucket per backend. When empty, handler blocks (does not ack). Redelivery retries. Effect: slow handler.
2. **Per-stage HPA (pod pool).** Autoscales on input-topic lag. Capped at AI-capacity ceiling. Effect: absorbs bursts within capacity.
3. **Scheduler admission gate (emission).** `pkg/backpressure.Gate` consults all downstream topics' `estimated_drain_time`. When any stage drain > 15 min **and** its HPA is at ceiling, gate admits less. Effect: crawl rate slows.

### 8.3 `pkg/backpressure.Gate` contract

Extended from the current gate:

```go
type Gate interface {
    // Admit asks to emit `want` events on `topic`. Returns how many are
    // granted (0 ≤ granted ≤ want) and an optional wait hint. If zero is
    // granted, the caller should defer the work; never drop.
    Admit(ctx context.Context, topic string, want int) (granted int, wait time.Duration)

    // UpdateLag is called by a telemetry collector (Prometheus scraper
    // or Frame pubsub hook) to push current stats per topic.
    UpdateLag(topic string, depth int64, consumeRate float64)

    // Config sets per-topic thresholds.
    Config(topic string, policy Policy)
}

type Policy struct {
    MaxDrainTime        time.Duration // throttle begins above this
    HardCeilingDrain    time.Duration // admit=0 above this
    HPACeilingKnown     bool          // if true, only throttle when HPA saturated
}
```

Only the scheduler calls `Admit`. Only the telemetry collector calls `UpdateLag`. Workers don't interact with the gate — their own rate limiters + HPA do the right thing.

### 8.4 Invariants

- Pub/sub must be durable (Frame config: persistent queues with ack).
- Dead-letter topics exist **only** for unrecoverable errors (parse failures, schema violations). Never for timeouts or rate limits.
- No codepath anywhere has a `if overloaded { skip_llm() }` branch.
- Acking happens only after the handler's complete success (or dead-lettering on unrecoverable).
- The writer acks a batch only after R2 upload ETag is confirmed.

## 9. Failure modes and recovery

### 9.1 Writer pod crash mid-batch

Effect: unacked events redeliver to other writer pods; some events appear in multiple Parquet files briefly.

Recovery: hourly compaction dedups by `event_id`; `canonicals_current/` rebuild on daily compaction makes it idempotent.

### 9.2 Materializer falls behind

Effect: Manticore lags R2. Serving freshness degrades from 60 s to minutes.

Recovery: HPA scales materializer pods on R2-poll lag; pods parallelize across partition prefixes. If a deploy or bug caused a days-long lag, reset materializer watermark to N-days-ago and it replays.

### 9.3 Manticore data corruption or lost index

Effect: serving returns stale/wrong data, or nothing at all.

Recovery: API returns 503 `Retry-After`; ops runs "Manticore rebuild from zero": wipe `idx_opportunities_rt`, reset materializer watermark to epoch 0, let it replay `*_current/` partitions. Hours at v1 volume.

### 9.4 KV lost (Valkey replica failure, region outage)

Effect: dedup misses → every variant looks new → cluster explosion → serving sees duplicates briefly.

Recovery: boot a new KV replica, run a rebuild job on an admin endpoint (`POST /_admin/kv/rebuild`):
1. Scan `canonicals_current/` partition.
2. For each row: `SET dedup:{hard_key} := cluster_id`; `SET cluster:{cluster_id} := snapshot`; update bloom.
3. Emit a "dedup:rebuilt" event so compaction knows to re-merge duplicate clusters created during the outage window.

Time: minutes (Parquet scans at gigabytes/sec).

### 9.5 R2 region outage

Effect: writer cannot upload → stops acking → pub/sub backlog grows. Materializer cannot read → Manticore stale. Serving fine (reads from Manticore).

Recovery: R2 returns → writer acks drain → materializer catches up.

### 9.6 LLM backend hard outage

Effect: validator fail-opens → variants accepted at 0.5 confidence with `validation_notes="LLM unavailable: …"`. Extractor fails on crawler → variants dropped (connector emits zero jobs for that page, retries next cycle). Embedder holds → embeddings backlog builds.

Recovery: backend returns → validator normal operation resumes; a background job can re-score `validation_score=0.5` variants after the fact by replaying them through validate.

### 9.7 Postgres outage

Effect: scheduler can't query `ListDue` → Trustage tick no-ops. User-facing search still works (Manticore + KV). Saved-jobs operations fail (503). Billing/auth fails (503 on login).

Recovery: Postgres returns → normal operation resumes. No data loss; all jobs data is in R2.

### 9.8 Pub/sub outage

Effect: pipeline halts; crawler cannot emit; handlers cannot consume. Serving continues (Manticore + KV unaffected).

Recovery: pub/sub returns → pipeline resumes from last acks. Durable queues preserve all in-flight work.

### 9.9 Trustage outage

Effect: no scheduled work fires; crawling stops (scheduler tick stops); compaction stops; retention stops. Live event pipeline unaffected.

Recovery: Trustage returns → tick resumes. A long outage means compaction backlog (many small Parquet files) — compactor's first run after recovery is larger.

## 10. Testing strategy

### 10.1 Unit tests (existing patterns)

- Per-handler tests stub pub/sub emissions and AI calls (mock `extraction.Extractor` methods). Preserves current `pkg/pipeline/handlers/*_test.go` shape.
- Parquet writer tests: buffer semantics, flush triggers, partition key routing, ack-after-upload ordering — use in-memory Parquet writer + fake R2.
- Backpressure gate tests: feed synthetic lag, verify admission behaviour at thresholds; verify HPA-ceiling awareness.

### 10.2 Integration tests (testcontainers, existing convention)

- `manticore`, `valkey`, `minio` (R2-compatible), Frame's in-memory pub/sub.
- End-to-end flow: synthetic variant → normalize → validate (mock LLM) → dedup → canonical → embedder (mock Embed) → writer → R2 → materializer → Manticore → query via API handler → assert result.
- Idempotency: run the same variant twice; assert exactly one cluster, one canonical, one Manticore row.
- Disposability: kill writer pod mid-batch; assert no data loss in final Parquet + Manticore state.

### 10.3 Chaos tests (new)

- Kill random pods during a synthetic crawl burst; assert zero event loss in the R2 log.
- Restart Manticore during ingest; assert materializer catches up when it returns.
- Wipe KV entirely; assert rebuild-from-Parquet admin endpoint restores `dedup:*`, `cluster:*`, bloom correctly.
- Inject LLM 429s on validator; assert validator retries, no fail-open, no drops.
- Inject LLM 503s on validator; assert validator fail-opens, variants flow through with `validation_score=0.5`.

### 10.4 Load / scale validation

- Generate 10 M synthetic variants over 24 h; assert end-to-end freshness stays under 90 s (p95); assert R2 Parquet footprint roughly matches expected (~10 GB at avg 1 KB/variant row); assert Manticore query p95 stays <100 ms under sustained load.
- Repeatable via a generator pod emitting `jobs.variants.ingested.v1` at configurable QPS.

### 10.5 AI determinism

- Golden-file tests per LLM prompt (validate, extract, translate, CV-extract) with recorded model responses to assert parser robustness on real-world shapes (markdown-fenced JSON, prose-wrapped JSON, truncated responses — the existing `extractJSONPayload` handles these; tests guard regression).

## 11. Rollout plan (greenfield + full-stack v1)

### 11.1 Pre-cut

1. Provision managed infra: Manticore cluster, Valkey cluster, R2 bucket (`opportunities-log`), TEI chat/embed/rerank deployments.
2. Define Trustage triggers (`definitions/trustage/scheduler-tick.json`, `compact-hourly.json`, `compact-daily.json`, `sources-quality-reset.json`, `sources-health-decay.json`, `matches-weekly-digest.json`, `cv-stale-nudge.json`).
3. Deploy new app skeletons with Postgres still as backend (no reads/writes to new stack yet).
4. Run pub/sub topic creation migrations; verify Frame subscriptions and DLQs.

### 11.2 Cut

1. Put the site into a brief maintenance banner ("search refreshing — back in minutes").
2. Drop Postgres tables: `canonical_jobs`, `job_variants`, `job_clusters`, `job_cluster_members`, `crawl_page_state`, `rerank_cache`, `mv_job_facets`. Drop the CV/preferences/embedding columns on `candidates`.
3. Flip `apps/api` to read from Manticore (empty at this moment — returns zero results).
4. Start the six services: `apps/crawler` (extended), `apps/worker` (new), `apps/writer` (new), `apps/materializer` (new), `apps/matching` (extended), `apps/api` (modified).
5. Trustage fires `scheduler.tick`; crawl-requests flow; pipeline fills; Manticore populates.
6. Lift maintenance banner once `idx_opportunities_rt` count > a sanity threshold (e.g., 50k jobs across top 10 countries).

### 11.3 Post-cut verification

| Check | Tool |
|---|---|
| Search returns results for 20 common queries | k6 / curl |
| Facets populate for category/country/remote_type | API fixture |
| CV upload round-trips (upload → extracted event → score report endpoint) | integration test |
| Match-for-candidate endpoint returns top-20 within 500 ms p95 | load test |
| Backpressure gate engages under synthetic burst | chaos test |
| Manticore rebuild-from-R2 procedure rehearsed | chaos test |
| KV rebuild-from-R2 admin endpoint rehearsed | chaos test |

### 11.4 v1.1 follow-up (deferred from v1)

- Build `idx_candidates_rt` in Manticore (schema parallel to `idx_opportunities_rt` with CV-derived fields).
- Extend materializer to index candidate partitions.
- Build recruiter API surfaces: free-text candidate search, saved searches, candidate-by-id export.
- Job → candidate matching fanout: triggered by `jobs.canonicals.upserted.v1`, query `idx_candidates_rt`, emit notifications.

No Parquet schema changes required — candidate data is already logged in v1.

## 12. Open questions / non-decisions

### 12.1 Embedding dimension (`DIM`)

Chosen by the configured embedding provider. `text-embedding-3-small` = 1536; `bge-m3` = 1024; others vary. `DIM` is a Manticore schema constant — must match the embedder config; changing it = rebuild the HNSW index. Pin in `definitions/config/embedder.json` alongside the backend URL.

### 12.2 Partition prefix granularity for `*_current/`

`cluster_id_prefix_2hex` (256 buckets) is the default. If partition files grow too large (>1 GB), bump to `3hex` (4096 buckets) during compaction. No schema change — purely a layout concern.

### 12.3 Reranker pre-warmer

A `rerank-warmer` Trustage trigger could pre-populate the KV with reranker output for trending queries, reducing cold-start latency on the paid tier. Not in v1; add in v1.1 if cold-start is user-visible.

### 12.4 Candidate page state (cursors)

The `domain.Source.config` column already holds opaque JSON cursors. The legacy `crawl_page_state` table (per-URL state) goes away in the event-sourced model — cursors are either on the source row (for paginated APIs) or implicit (for per-URL requests). One follow-up: confirm no connector depends on the per-URL table (quick audit of `pkg/connectors/*`).

### 12.5 Manticore deployment topology

Single-node RT is fine at v1 scale (<10 M jobs). Sharding/replication is a v1.1+ scale question; the API already treats Manticore as a remote service so plugging in an L4 balancer later is config-only.

### 12.6 Audit log retention

`variants/`, `canonicals/`, `match_decisions/`, `jobs_expired/` grow forever. Retention policy not yet defined; at current rates a daily Parquet partition is ~500 MB, so a year of history is ~200 GB — cheap on R2. A Trustage `retention.archive` trigger can later roll old partitions into a cold tier if needed. Not v1.

### 12.7 Multi-region R2

Not in v1 scope. If added later: R2 has native cross-region replication; writer targets one region, materializer and consumers read closest.
