# Continuous Matching + Applications API Design

**Date:** 2026-05-20
**Status:** Draft (pending user review)
**Author:** Brainstorming session, codified from approved sections 1–5.

## Goals

1. **Continuously and correctly match opportunities** to candidates at growth-stage scale (~1M candidates, ~100K live opportunities) — push-style freshness for high-confidence matches, self-healing on rule/CV changes, bounded per-request and per-event work.
2. **Provide a simple, exhaustive applications API** for a browser extension that performs ATS form submission client-side on the user's behalf, under user-configured rules.
3. **Thorough tracking** of every application: state history, notes, attachments, reminders. Single source of truth for "what did the user do, when, with what evidence."
4. **Drop pre-launch legacy** — flat `/api/*` namespace (no version segment), retire the manticore-cutover scaffolding.

## Non-goals

- Server-side form-submission automation (the browser extension handles it client-side).
- A recruiter / employer-side console — out of scope here.
- Real-time push delivery (SSE / WebSocket / FCM) — polling is the MVP channel; push can be layered on later.
- Multi-region or sharded deployment — single-primary Postgres is sized for the target scale.

## Constraints

- TimescaleDB hypertables preferred for append-only / time-bucketed data (pattern already established in `pkg/variantstate`).
- Existing infra only: Postgres + pgvector + TimescaleDB, NATS JetStream, R2, Valkey. No new datastores.
- OIDC bearer auth via `@stawi/auth-runtime` (no separate API tokens).
- Project conventions: Frame framework, three-layer architecture, `util.Log(ctx)`, `datastore.BaseRepository`, `data.BaseModel`.

---

## 1. System architecture & service boundaries

```
                ┌────────────── Browser extension ──────────────┐
                │  reads matches, writes application states,    │
                │  performs the actual ATS form submission      │
                └─────────────────────┬──────────────────────────┘
                                      │  /api/* (HTTPS, OIDC bearer)
                                      ▼
┌───────────────┐   ┌──────────────────┐   ┌──────────────────────┐
│  apps/api     │   │  apps/matching   │   │  apps/applications   │
│  (read API)   │   │  (matchers +     │   │  (NEW: app lifecycle │
│  feed, search │   │  fan-out workers)│   │  CRUD + audit log)   │
└───────┬───────┘   └────────┬─────────┘   └──────────┬───────────┘
        │                    │                        │
        ▼                    ▼                        ▼
              ─────── Postgres + pgvector + R2 ───────
        ▲                    ▲                        ▲
        │                    │                        │
   (existing: crawler / worker / materializer → opportunities)
                             │
                       NATS JetStream
              (CanonicalUpsertedV1, CandidateEmbeddingV1,
               PreferencesUpdatedV1, ApplicationStateChangedV1)
```

Three Go services in the existing `apps/` layout.

- **`apps/api`** — read façade (search, feed, public endpoints). No change of role; gains the rename from `/api/v2/*` to `/api/*`.
- **`apps/matching`** — gains fan-out workers, candidate-side vector index, reranker integration, and the new `/api/me/matches*` HTTP routes.
- **`apps/applications` (new)** — owns the applications domain. Own HTTP surface (`/api/me/applications*`), own JetStream consumers, own Postgres tables. Separated from `apps/matching` because "find a match" and "track what the user did with the match" have different scaling profiles (matching is write-heavy + compute-heavy; applications is OLTP CRUD).

R2 stays for attachments + CV blobs (new key prefix `applications/{candidate_id}/{application_id}/…`). No new infrastructure.

---

## 2. Data model

Rule applied throughout: **append-only / audit / analytics → TimescaleDB hypertable; current-state / OLTP CRUD → regular table.** Hypertables receive retention + compression policies. Each hypertable's PK includes the partition column (TimescaleDB requirement).

### 2.1 Hypertables (new)

| Table | Partition col | PK | Retention | Compression | Purpose |
|---|---|---|---|---|---|
| `candidate_match_events` | `occurred_at` | `(event_id, occurred_at)` | 365d | >7d | Append-only log of every match generated, viewed, dismissed. Powers analytics, "why was this matched" forensics, time-bucket dashboards. |
| `application_events` | `occurred_at` | `(event_id, occurred_at)` | indefinite | >14d | Canonical audit log for application lifecycle (state transitions, extension submission attempts, recruiter responses recorded by user). Drives `applications` current-state via projection. |
| `engagement_events` | `occurred_at` | `(event_id, occurred_at)` | 180d | >7d | Beacon traffic: view, click, dismiss, apply. Replaces the current Valkey-counter + OpenObserve dual-write pattern. 24h counters served via continuous aggregate. |
| `match_run_events` | `started_at` | `(run_id, started_at)` | 90d | >7d | Per-run telemetry: triggered_by (fan-out / gap-filler / rules-changed), candidates_scanned, latency, errors. Operational only. |

Each hypertable gets a continuous aggregate (`CREATE MATERIALIZED VIEW … WITH (timescaledb.continuous)`) where rollups matter — e.g. `engagement_events_hourly` for the 24h apply/view counters that today live in Valkey.

### 2.2 Regular tables (new)

| Table | PK | Purpose |
|---|---|---|
| `candidate_matches` | `match_id` | Current state of every match surfaced to a candidate. UNIQUE on `(candidate_id, opportunity_id)`. Mutable: `status` (new/viewed/dismissed/applying/applied/overflow), `score`, `rerank_score`, `viewed_at`, `applied_at`, `last_event_id`. |
| `applications` | `application_id` | Current state of every application. UNIQUE on `(candidate_id, opportunity_id)`. Mutable: `status`, `current_stage`, `metadata` (JSONB), `submitted_at`, `last_event_id`. |
| `application_notes` | `note_id` | User-authored notes, many-to-one under `application_id`. |
| `application_attachments` | `attachment_id` | Pointer to R2 object: `r2_key`, `content_type`, `bytes`. Many-to-one under `application_id`. |
| `application_reminders` | `reminder_id` | `(application_id, due_at, status)`. Active rows only; completed reminders fire an `application_events` row and get archived. |
| `match_rules` | `candidate_id` (1:1) | The user's autonomy rules. JSONB blob (schema below). |
| `candidate_match_indexes` | `candidate_id` | Materialized vector + filter bag for fan-out lookups: `embedding` (pgvector `vector(1536)`), `min_score`, `daily_cap`, `countries[]`, `kinds[]`, `enabled`. Rebuilt on CV or rules change. HNSW index. |
| `extension_grants` | `grant_id` | One row per browser-extension install issued to a candidate. Carries `candidate_id`, `extension_install_id` (claim asserted in JWT), `user_agent`, `issued_at`, `revoked_at`. Supports per-install revocation without nuking the user's web session. |

### 2.3 Schema deltas

- `opportunities` — add composite index `(status, hidden, posted_at DESC) WHERE status='active' AND hidden=false` for fan-out filter.
- `candidate_profiles` — drop the in-row `embedding` column (moved to `candidate_match_indexes`). Keep `last_matched_at` for dashboards.
- Legacy `candidate_matches` (old table) — superseded; migration projects existing rows into the new table.

### 2.4 Rules document schema

Stored as JSONB on `match_rules`. Validated against a JSON Schema in `pkg/applications/rules.go`. Unknown fields rejected.

```json
{
  "version": 1,
  "enabled": true,
  "min_score": 0.62,
  "daily_cap": 25,
  "weekly_cap": 100,
  "kinds": ["job", "scholarship"],
  "countries": ["KE", "UG", "TZ", "remote"],
  "salary_floor_usd": 30000,
  "remote_only": false,
  "dismiss_after_days": 14,
  "blocklist": {
    "companies": ["AcmeCo"],
    "domains": ["example.com"]
  },
  "autoapply": {
    "enabled": true,
    "require_min_score": 0.78,
    "kinds": ["job"]
  }
}
```

Two enable toggles by design: `enabled` controls whether matches surface; `autoapply.enabled` controls whether the extension is allowed to submit on behalf of the user.

---

## 3. Event flow & matching pipeline

Three write paths converge on `candidate_matches`. All three share the scoring function in `pkg/matching/score.go`, the idempotency invariants, and the telemetry surface. The extension only ever reads matches — never writes them.

### 3.1 Path A — Fan-out (write-time push)

Triggered by `CanonicalUpsertedV1` on JetStream from the materializer.

```
1. Load opportunity from Postgres.
2. Skip if status != 'active' or hidden = true.
3. KNN-and-filter query against candidate_match_indexes:
       SELECT candidate_id, score
       FROM candidate_match_indexes
       WHERE enabled = true
         AND opportunity.kind = ANY(kinds)
         AND (opportunity.country IS NULL
              OR opportunity.country = ANY(countries))
         AND (opportunity.salary_max IS NULL
              OR opportunity.salary_max >= salary_floor)
       ORDER BY embedding <=> $opp_embedding
       LIMIT 500;
4. Score each candidate (cosine + structured boosts).
5. Drop anything below rules.min_score AND global floor.
6. Optional reranker on top 50 (config; default off for fan-out).
7. Bulk UPSERT into candidate_matches with
   ON CONFLICT (candidate_id, opportunity_id)
   DO UPDATE SET score = GREATEST(...), last_event_id = ...
   WHERE candidate_matches.status = 'new'.
8. Bulk INSERT into candidate_match_events.
9. Enforce daily_cap: rows beyond cap stored as status='overflow'
   (queryable, hidden from default polls).
```

**Concurrency.** JetStream durable consumer; `MaxAckPending = N_workers × 8`; work-stealing across workers. With 100K opportunities/day and top-500 KNN per opportunity, this is ~50M scored rows/day, comfortably served by HNSW pgvector (`ef_search = 200`) on a single primary + read replica.

**Idempotency.** Events carry `canonical_id` + `version`. `ON CONFLICT … WHERE status = 'new'` ensures terminal states (`applied`, `dismissed`) are never downgraded. Replays are safe.

**Why `LIMIT 500` / top-50 rerank.** HNSW top-500 is ~10ms p95; reranking 50 items with a local cross-encoder is ~150ms. The fan-out path optimizes for recall into the candidate's match bucket — read-time ordering is the reranker's home.

### 3.2 Path B — Gap-filler (read-time pull)

Triggered by the extension calling `GET /api/me/matches?since=<cursor>`.

```
1. Read candidate's embedding + rules from candidate_match_indexes.
2. Bounded freshness scan:
       SELECT opportunity_id, embedding
       FROM opportunities
       WHERE status='active' AND hidden=false
         AND first_seen_at > $since
         AND kind = ANY($rules.kinds)
         AND (country IS NULL OR country = ANY($rules.countries))
       ORDER BY embedding <=> $cand_embedding
       LIMIT 100;
3. Score + rerank (top 20 — read path can afford it).
4. UPSERT into candidate_matches with status='new'
   (anything fan-out already wrote stays untouched).
5. Append candidate_match_events rows.
6. Return merged page from candidate_matches, paginated by event_id.
```

Gap-filler covers three cases: matches the fan-out skipped (overflow, score-below-threshold-at-fan-out-but-relevant-after-rerank), recovery if fan-out workers are paused, and the on-demand "show me anything new" UX.

### 3.3 Path C — Candidate-side change

Triggered by `PreferencesUpdatedV1`, `CandidateEmbeddingV1`, or a manual `POST /api/me/rules`.

Same pipeline as Path A but **inverted**: "one candidate → top-500 opportunities," scored against the candidate's current embedding/rules, written into `candidate_matches`. This is the recovery path for rule loosening and the bootstrap path for new sign-ups. Debounced at most once per N minutes per candidate (Valkey lock keyed on `candidate_id`).

### 3.4 Scoring function

```
score = w1 · cosine(cand.embedding, opp.embedding)          [0..1]
      + w2 · skills_overlap(cand.skills, opp.skills)        [0..1]
      + w3 · geo_match(cand.countries, opp.country)         [0|1]
      + w4 · salary_fit(cand.salary_floor, opp.salary_max)  [0..1]
      − p1 · stale_penalty(opp.first_seen_at)               [0..1]
```

Lives in `pkg/matching/score.go`. Same function called from every path. Weights are env config, not code. `cosine` is always present; the others default to neutral when source data is missing.

### 3.5 Idempotency & dedup invariants

1. **`(candidate_id, opportunity_id)` unique** on `candidate_matches` — at most one row per pair.
2. **`(canonical_id, candidate_id, event_kind, event_version)` deduped by hash** in `candidate_match_events`.
3. **`ON CONFLICT DO UPDATE WHERE status = 'new'`** — terminal states protected from late events.
4. **Score-monotonic update** — keep the higher score and the latest `last_event_id` when paths overlap.

### 3.6 Rate limiting & backpressure

- Per-candidate daily cap enforced from continuous aggregate on `candidate_match_events`.
- Fan-out worker concurrency via `MATCHING_FANOUT_WORKERS` env; backpressure from JetStream `MaxAckPending`.
- Per-candidate gap-filler debounce (Valkey lock, 60s default).
- Reranker pool bounded; if full, fall back to retrieval-only score.

### 3.7 Failure handling

| Failure | Behavior |
|---|---|
| KNN query timeout | Ack event, log to `match_run_events` with `status='timeout'`, alert on rate. Opportunity reattempted via Path B. |
| Reranker down | Skip rerank, use retrieval score, log `reranker_status='skipped'`. |
| DB primary failover | JetStream redelivery + idempotency invariants. |
| Poison-pill event | After 5 redeliveries, DLQ `svc.opportunities.matching.deadletter`. Admin replay endpoint. |
| Corrupt candidate embedding | Worker logs + skips; nightly healing job. |

---

## 4. API surface (extension-facing) & rules engine

### 4.1 Conventions

- Flat `/api/*`, no version segment. Reserved namespaces: `/api/me/*` (per-candidate, OIDC bearer), `/api/public/*` (anonymous), `/api/admin/*` (operator).
- `Idempotency-Key` header on every mutating request, stored 24h in Valkey, scoped to `(candidate_id, route_group)`.
- JSON responses. `application/problem+json` (RFC 9457) errors with `type`, `title`, `status`, `detail`, `instance`.
- `ETag` + `If-None-Match` on read endpoints the extension polls (cursor-based on matches).
- Pagination: `next_cursor`, `has_more`, optional `total` (omitted by default — expensive on hypertables).
- 429s return `Retry-After`. Token-bucket rate limits per `(candidate_id, route_group)`.

### 4.2 Read endpoints

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/me` | Candidate profile bag (id, display name, subscription tier, opt-in state). Cacheable 60s. |
| GET | `/api/me/matches` | Primary extension endpoint. Paginated match feed. Query: `since=<event_id>`, `kind=`, `status=` (default `new,viewed,applying`), `limit=` (default 50, max 200). |
| GET | `/api/me/matches/{match_id}` | Single match with full opportunity body + scoring breakdown. |
| GET | `/api/me/rules` | Canonical autonomy rule document. |
| GET | `/api/me/applications` | Paginated current-state list. Filters: `status`, `kind`, `from`, `to`, `sort` (`-submitted_at` default). |
| GET | `/api/me/applications/{application_id}` | Detail with notes + attachments + last 10 events embedded. |
| GET | `/api/me/applications/{application_id}/events` | Full audit history, paginated by event_id. |
| GET | `/api/me/reminders` | Active reminders sorted by `due_at`. |
| GET | `/api/me/profile-fields` | Structured CV/profile data for the extension to autofill ATS forms. Versioned with `ETag`. |
| GET | `/api/me/attachments/{attachment_id}` | Pre-signed R2 download URL (15min TTL, single-use). |

### 4.3 Write endpoints

All require `Idempotency-Key`. Server enforces `(candidate_id, idempotency_key)` uniqueness.

| Method | Path | Purpose |
|---|---|---|
| PUT | `/api/me/rules` | Replace autonomy rule document. Triggers Path C, debounced. |
| POST | `/api/me/matches/{match_id}/dismiss` | Mark dismissed. Writes `candidate_match_events` `kind=dismissed`. Row preserved. |
| POST | `/api/me/matches/{match_id}/view` | Beacon for "user looked at this match." Writes `engagement_events` + updates `viewed_at`. 204. |
| POST | `/api/me/applications` | Extension declares an application. Body `{match_id, status?, metadata?}`. Idempotent on `(candidate_id, opportunity_id)`. Creates `applications` row, transitions `candidate_matches.status='applying'`. |
| PATCH | `/api/me/applications/{application_id}` | Update `status`, `current_stage`, `metadata`. State machine validated. Writes `application_events`. |
| POST | `/api/me/applications/{application_id}/events` | Append an event (`submission_attempted`, `submission_succeeded`, `submission_failed`, `recruiter_replied`, etc.). Validated against allowed-set; applies state transition if applicable. |
| POST | `/api/me/applications/{application_id}/notes` | Add free-text note. |
| PATCH | `/api/me/applications/{application_id}/notes/{note_id}` | Edit a note. Append `kind=note_edited` event (no destructive update). |
| DELETE | `/api/me/applications/{application_id}/notes/{note_id}` | Soft-delete. |
| POST | `/api/me/applications/{application_id}/attachments` | Multipart upload (max 25MiB). Server PUTs to R2, writes attachment row + event. |
| DELETE | `/api/me/applications/{application_id}/attachments/{attachment_id}` | Soft-delete. R2 object retained 30d. |
| POST | `/api/me/applications/{application_id}/reminders` | Body `{due_at, note?}`. |
| PATCH | `/api/me/applications/{application_id}/reminders/{reminder_id}` | Snooze (`due_at`) or ack (`status='done'`). |
| DELETE | `/api/me/applications/{application_id}/reminders/{reminder_id}` | Soft-delete. |

### 4.4 Application state machine

```
            ┌─→ dismissed (terminal)
   new ─────┤
            └─→ applying ──→ submitted ──→ screening ──→ interview ──→ offer ──→ accepted (terminal)
                                                                            ├─→ rejected  (terminal)
                                                                            └─→ withdrawn (terminal)
                                  ↓                ↓             ↓             ↓
                              (any non-terminal can drop to rejected or withdrawn)
```

Enforced server-side in `pkg/applications/state.go` transition table. Invalid transitions return `409 Conflict` with the allowed set in the error body. The extension never decides validity — server is the only authority.

### 4.5 Authentication

OIDC bearer via `@stawi/auth-runtime`. The extension performs the same OIDC flow as the web app and attaches `Authorization: Bearer …` to every request. JWT carries `extension_install_id` claim so a specific install can be revoked without affecting the user's web session. An `extension_grants` table tracks issued installs.

### 4.6 Admin endpoints

| Method | Path | Purpose |
|---|---|---|
| POST | `/api/admin/matching/rebuild-index` | Recompute `candidate_match_indexes`. |
| POST | `/api/admin/matching/replay` | Re-emit `CanonicalUpsertedV1` for a canonical_id. |
| GET | `/api/admin/matching/dlq` | Inspect matching DLQ. |
| GET | `/api/admin/applications/{id}` | Read any application (support tools). |
| POST | `/api/admin/dlq/replay` | Replay specific message from a DLQ. |

Admin routes gated by an `admin` claim, not the user RBAC.

---

## 5. Error handling, observability, testing, rollout

### 5.1 Error categories

All errors classify into one of five buckets via `pkg/errors/category.go`:

| Category | Definition | Behavior |
|---|---|---|
| `transient` | Network blip, DB timeout, transient lock | JetStream redelivery backoff (1s → 5s → 30s → 5m → 30m, max 5); HTTP 503 with `Retry-After`. |
| `dependency` | External service unavailable (R2, OIDC issuer) | Circuit breaker (`pkg/util/circuit`); degraded best-effort response. |
| `invalid_input` | Bad request shape, schema or state-transition violation | 400 / 409 with `problem+json`. Never retried. |
| `not_found` | Missing entity | 404 with entity kind. |
| `internal` | Logic bug, invariant violation | ERROR log with full context, `app_errors_total{kind="internal"}` increment, page on rate breach. DLQ after redelivery exhaustion. |

Boundary validation only — internal helpers trust their inputs. Idempotency is the recovery mechanism for any partial failure.

### 5.2 Observability

**Tracing.** Every span carries: `canonical_id`, `candidate_id`, `opportunity_id`, `match_id`, `application_id`, `path` (fanout / gap / candidate_change / user_action), `decision`, `score`, `rerank_score`, `reranker_used`. NATS trace-context propagation already exists in `pkg/messaging`. Extension sends `traceparent` so end-to-end traces span extension → API → DB.

**Metrics (Prometheus via Frame's `util.Metrics`).**

| Metric | Type | Labels |
|---|---|---|
| `matches_written_total` | counter | `path`, `kind`, `status` |
| `match_score_bucket` | histogram | `path`, `kind` |
| `match_latency_seconds` | histogram | `path`, `phase` (retrieval / score / rerank / write) |
| `applications_state_transitions_total` | counter | `from`, `to`, `kind` |
| `applications_open` | gauge | `status`, `kind` (refreshed from continuous aggregate) |
| `extension_requests_total` | counter | `route_group`, `status_code` |
| `extension_idempotency_replay_total` | counter | `route_group` |
| `dlq_depth` | gauge | `subject` |
| `reranker_pool_in_use` | gauge | — |
| `candidate_match_indexes_stale_total` | counter | `cause` |

**Logging.** Structured JSON via `util.Log(ctx)`. INFO for state transitions, WARN for transient failures, ERROR for internal/DLQ. Hot-path logging sampled at 0.1% via the existing sampler. PII fields rejected at compile time via `pkg/util/loglint`.

**Dashboards (Grafana, in `deployments/observability`).**

- *Matching health*: fan-out lag, write rate per path, score distribution, reranker pool, DLQ.
- *Applications funnel*: created → submitted → response → outcome conversion rates, sliced by kind/country.
- *Extension health*: requests/sec, p95 latency, idempotency replays, 4xx/5xx ratio per route group.
- *Per-candidate hotspot*: top 50 candidates by `matches_today` + `applications_today`.
- *Hypertable health*: chunk count, compression ratio, retention lag.

**Alerts (pageable).**

- `dlq_depth > 0` for 10m on either DLQ.
- `match_latency_seconds{p99,path="fanout"} > 5s` for 15m.
- `applications_state_transitions_total{from="submitted",to="rejected"} / total > 0.4` for 24h.
- `candidate_match_indexes_stale_total > 1000` per minute.
- HTTP 5xx rate > 1% on any `/api/me/*` route for 5m.

### 5.3 Testing strategy

**Unit.** `pkg/matching/score_test.go`, `pkg/applications/state_test.go`, `pkg/applications/rules_test.go`. Pure functions, table-driven. Target: 100% of state transitions, 100% of rule validations, all scoring branches.

**Integration via testcontainers** (`tests/integration`, reusing `BaseTestSuite`):

- `MatchingPipelineSuite` — all three paths (A, B, C). Idempotency replay (3× publish → 1 row), score-monotonic update, terminal-state immunity, daily-cap overflow.
- `ApplicationsLifecycleSuite` — full state machine end-to-end via HTTP. Invalid transitions → 409.
- `RulesChangeSuite` — Path C debounce, loosening triggers backfill, tightening preserves prior matches.
- `ExtensionContractSuite` — golden-file response shapes (etag, cursors, pagination).
- `ConcurrencySuite` — 50 concurrent fan-out workers, 1000 events; assert no dupes.
- `RetentionAndCompressionSuite` — fast-forward Timescale chunk time, assert policies fire.

All suites pass with `-race`. CI gate.

**Property tests.** `pkg/applications/state.go` gets a `pgregory.net/rapid` test asserting any sequence of valid transitions from `new` lands in a state the graph declares reachable.

**Load test (`tests/load/matching.go`, k6 + Go harness).**

Simulating: 100K opportunities/day, 50K active candidates polling every 5min, 10K rule changes/day, 5K application writes/day.

Targets:
- Fan-out p95 < 1s
- `GET /api/me/matches` p95 < 300ms
- DB CPU < 60% on a 4-core primary

Weekly CI run against staging cluster.

**Contract tests for the extension.** Server publishes OpenAPI on `/api/openapi.json`; `tests/contract/extension_contract_test.go` runs OpenAPI diff against the extension repo's pinned spec. Drift fails CI.

### 5.4 Security & privacy

- CV blobs in R2: Cloudflare encryption at rest, pre-signed URLs 15min TTL, single-use enforced via in-memory bloom filter.
- `profile-fields` endpoint redacts secrets (no government IDs, no DOB unless explicitly opted in for scholarship eligibility — kind-specific opt-in).
- Attachments scoped to `candidate_id`; signed URLs include `application_id` for audit.
- Extension JWTs carry `extension_install_id`; per-install revocation supported.
- PII fields (`email`, `phone`, `cv`, `address`) rejected by `pkg/util/loglint` at compile time.
- Rate-limit on `POST /api/me/applications` at 200/day/candidate (extension-misbehavior cap).

### 5.5 Migration & rollout

Five-step rollout, each independently revertible.

**Step 1 — Tables + indexes (zero traffic).**
Migrations starting at `0009` create the new schema: `candidate_match_events`, `application_events`, `engagement_events`, `match_run_events` (hypertables), `applications`, `application_notes`, `application_attachments`, `application_reminders`, `match_rules`, `candidate_match_indexes`, `extension_grants`. Add the opportunities composite index. Backfill `candidate_match_indexes` from `candidate_profiles`. One migration file per logical group (events/applications/rules/indexes/grants) — exact numbering settled in the implementation plan.

**Step 2 — Stand up `apps/applications`.**
Deploy with routes registered behind `APPLICATIONS_ENABLED=false`. Run integration suites in staging. Flip the gate when green.

**Step 3 — Continuous matching paths.**
Deploy fan-out workers behind `MATCHING_FANOUT_ENABLED=false`. Dry-run mode (`MATCHING_FANOUT_DRY_RUN=true`) does everything except writes; compare expected vs actual against the legacy weekly sweep. Roll to 1% candidates, then 100% over a week. Retire the weekly cron after one full coverage cycle proves equivalent.

**Step 4 — Migrate legacy `candidate_matches` rows.**
One-shot migration projects legacy rows into the new table (status, score, timestamps preserved). Drop legacy schema after a 30-day quiet window.

**Step 5 — API rename `/api/v2/*` → `/api/*` and delete legacy.**
- Add new routes; keep v2 routes live and 308-redirecting to the new path for two deploys (≤ a week).
- UI cut over in the same PR.
- After the window: **delete** `apps/api/cmd/endpoints_v2.go`, `apps/api/cmd/endpoints_v2_test.go`, `tests/integration/cutover_e2e_test.go`. Rename handler funcs to drop `v2` prefix.

### 5.6 Legacy items to delete

Pre-launch, nothing requires backwards compatibility. The following are removed as part of this work:

| Item | Why | Step |
|---|---|---|
| `apps/api/cmd/endpoints_v2.go` | v2 prefix was a manticore-cutover artifact. Renamed → `endpoints.go`, prefix dropped. | Step 5 |
| `apps/api/cmd/endpoints_v2_test.go` | Same as above; renamed → `endpoints_test.go`. | Step 5 |
| `tests/integration/cutover_e2e_test.go` | Existed only to validate the manticore→postgres cutover (`bc76828`, `141d3b8`). Cutover complete; test purpose served. **Delete.** | Step 5 |
| Weekly digest sweep (`apps/matching/service/admin/v1/matches_weekly.go`) | Subsumed by continuous fan-out + gap-filler. **Delete after Step 3 stable.** | Step 3 |
| Manticore search backend (`SEARCH_BACKEND=manticore` branch in `pkg/search` and related connectors) | Postgres default since `141d3b8`. Rollback window passed. **Delete fully** including env flag, connectors, and admin endpoints that only lit up on the legacy backend. | Step 5 |
| Legacy `candidate_matches` table | Superseded by the new schema. Migration projects rows; drop after 30-day quiet window. | Step 4 |
| `candidate_profiles.embedding` column | Moved to `candidate_match_indexes`. Drop after Step 3 stable. | Step 3 |

### 5.7 Capacity & rollback

| Resource | Headroom plan |
|---|---|
| Postgres primary | Add read replica before Step 3 (gap-filler reads + dashboards offloaded). |
| pgvector HNSW | Pre-built on `candidate_match_indexes`; one-shot rebuild job; staleness alarm. |
| JetStream | New consumers on existing stream; no new stream needed. |
| Workers | Single deployment, horizontal autoscale on consumer lag. |
| R2 | Attachments share existing content bucket under `applications/{candidate_id}/{application_id}/…`. |

**Rollback.** Each step has a flag (`MATCHING_FANOUT_ENABLED`, `APPLICATIONS_ENABLED`, `API_LEGACY_V2_ENABLED`). Flipping any flag back is single-deploy. Step 4 (table rename) requires manual revert of the migration; the 30-day quiet window is specifically to keep that recoverable.

---

## 6. Acceptance criteria

The system is considered complete when:

1. A new `CanonicalUpsertedV1` event lands a row in `candidate_matches` for every eligible candidate within p95 < 1s (fan-out path), with a corresponding `candidate_match_events` row.
2. `GET /api/me/matches?since=<cursor>` returns p95 < 300ms with combined fan-out + gap-filler coverage; the same cursor returned twice in succession yields identical results modulo time-bounded new arrivals.
3. The full application state machine is walkable end-to-end via `/api/me/applications*`; every transition writes an `application_events` row; invalid transitions return 409 with the allowed set.
4. The browser extension can: (a) read its match feed, (b) read autofill profile data, (c) declare an application, (d) append submission events with idempotency, (e) attach evidence — all through `/api/me/*`.
5. Idempotency replay tests pass: any mutating endpoint called with the same `Idempotency-Key` returns the same result and does not duplicate rows or events.
6. Hypertable retention + compression policies are configured and demonstrated by the `RetentionAndCompressionSuite`.
7. All legacy items in §5.6 are deleted; no `/api/v2` route remains; no manticore code path remains.
8. Load test targets in §5.3 are met against staging.

---

## 7. Open questions

None as of writing — every decision has a chosen option recorded in the relevant section. Questions that arise during planning should be appended here before the writing-plans transition.

## 8. Progress log

- **Phase 1 (foundation) — done.** Schema migrations 0009–0012 land four
  hypertables, the applications OLTP tables, match_rules,
  candidate_match_indexes, and extension_grants. Three pure packages
  (`pkg/matching/score`, `pkg/applications/state`,
  `pkg/applications/rules`) carry the scoring function, state machine,
  and rules schema validator. Integration test `TestMigrationsApplyCleanly`
  smokes the schema on a fresh TimescaleDB + pgvector container.

- **Phase 2 (matching pipeline) — done.** Migration 0013 lands the new
  OLTP `candidate_matches` (legacy GORM-managed table renamed to
  `candidate_matches_legacy`). `pkg/matching` gains the data-access
  layer (`Store`, `EventLog`, `IndexStore`, `KNN`) with monotonic-score
  + terminal-protected `ON CONFLICT … WHERE status='new'` UPSERT,
  cursor-paginated reads, and append-only event writers. Three pure
  orchestrators — `FanOut` (Path A), `GapFill` (Path B),
  `RunCandidateChange` (Path C) — share the deterministic scoring
  function and write through the same event log. JetStream consumers
  in `apps/matching/service/matching/v1` subscribe Path A to
  `TopicCanonicalsUpserted` and Path C to
  `TopicCandidatePreferencesUpdated` + `TopicCandidateEmbedding`,
  guarded by `MATCHING_FANOUT_ENABLED` /
  `MATCHING_CANDIDATE_CHANGE_ENABLED` env flags. Reranker is bounded
  + best-effort with retrieval-score fallback (`NoopReranker` default,
  `PooledReranker` when `MATCHING_RERANKER_ENABLED=true`). `DLQGuard`
  publishes to `svc.opportunities.matching.deadletter` after 5
  redeliveries via the Frame `QueueManager`. Prometheus collectors
  defined per §5.2. In-memory `MemoryDebouncer` ships as the Path-C
  TTL gate (Valkey-backed replacement in Phase 5). Integration
  coverage in `MatchingPipelineSuite`: Path A happy path, idempotent
  replay, terminal-state immunity, Path B merging without duplicates.

- **Phase 3 (applications service) — done.** New `apps/applications`
  binary (frame service + `http.NewServeMux`) flag-gated by
  `APPLICATIONS_ENABLED`. `pkg/applications` gains the data-access
  layer: `Store` (applications CRUD + state-machine-enforced
  `ApplyTransition`), `EventLog` (append-only writer for
  `application_events`), `NotesStore` / `RemindersStore` /
  `AttachmentsStore` with soft-delete, `IdempotencyStore`
  (pg-backed, 24h TTL, first-writer-wins), and the `BlobStore`
  interface with `MemoryBlobStore` for dev/test (R2 adapter in
  Phase 5). Migration 0014 adds `idempotency_keys` (the soft-delete
  columns were already in 0010). HTTP surface implemented in
  `apps/applications/service/http/v1`:
  `POST/GET/PATCH /api/me/applications`,
  `GET /api/me/applications/{id}` (with embedded event tail),
  `GET/POST /api/me/applications/{id}/events`,
  `POST/PATCH/DELETE /api/me/applications/{id}/notes(/{note_id})`,
  `POST/PATCH/DELETE /api/me/applications/{id}/reminders(/{rid})`,
  `POST/DELETE /api/me/applications/{id}/attachments(/{att_id})`,
  `GET /api/me/attachments/{att_id}` (pre-signed download),
  `GET /api/me/reminders`. Middleware: `CandidateAuth`
  (`X-Candidate-ID` for now — OIDC bearer landing later), pg-backed
  `Idempotency` (replays cached 2xx bytes), and RFC 9457
  `problem+json` errors that include the state-machine `allowed`
  set on 409 invalid transitions. Integration coverage in
  `ApplicationsLifecycleSuite`: full new → applying → submitted →
  screening → interview → offer → accepted walk with event audit;
  invalid transition surfaces 409 + allowed list; idempotency
  replay returns identical bytes; sub-resources emit their event
  kinds; cross-candidate isolation hides A's rows from B.

- **Phase 4 (extension-facing endpoints) — done.** New
  `apps/matching/service/http/me/v1` package wired into the matching
  binary's mux behind `MATCHING_EXTENSION_ENABLED`. Endpoints:
  `GET /api/me` (candidate id + autoapply state),
  `GET /api/me/matches` (paginated, status csv filter, cursor),
  `GET /api/me/matches/{match_id}`,
  `POST /api/me/matches/{match_id}/dismiss` (idempotent, writes
  `candidate_match_events`),
  `POST /api/me/matches/{match_id}/view` (writes
  `engagement_events`),
  `GET /api/me/rules`, `PUT /api/me/rules` (validated via
  `applications.ParseRules`, persisted via
  `matching.RulesStore`, triggers Path C via
  `matching.RunCandidateChange` best-effort),
  `GET /api/me/profile-fields` (loads `candidate_profiles` via
  `pkg/candidatestore.GetProfileFields` with stable
  `If-None-Match` / `ETag` round-trip).
  Shared middleware lifted to `pkg/httpmw` so both
  `apps/applications` and `apps/matching` consume the same
  `CandidateAuth` + `Idempotency` + `Problem`/`ProblemJSON`
  primitives. New `pkg/matching/rules_store.go` keeps the
  denormalized `enabled` / `autoapply` booleans in sync with the
  JSONB rules document. Integration suite in
  `apps/matching/service/http/me/v1/handlers_test.go` covers all
  endpoints + cross-candidate isolation + ETag conditional GET.
