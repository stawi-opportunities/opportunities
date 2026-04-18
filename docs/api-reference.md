# Stawi Jobs — API Reference

Comprehensive reference for every HTTP, Connect RPC, NATS, and OpenObserve surface that stawi-jobs services expose or consume.

## Contents

- [Services overview](#services-overview)
- [stawi-jobs-api](#stawi-jobs-api): public search, job detail, view beacon
  - [Public search & discovery](#public-search--discovery)
  - [View ingest & attribution](#view-ingest--attribution)
  - [Admin: republish & backfill](#admin-republish--backfill)
- [stawi-jobs-candidates](#stawi-jobs-candidates): profile, saved jobs, CV scoring, notifications
  - [Authenticated profile](#authenticated-profile)
  - [Saved jobs](#saved-jobs)
  - [CV Strength Report](#cv-strength-report)
  - [Internal signals](#internal-signals)
  - [Webhooks](#webhooks)
- [stawi-jobs-crawler](#stawi-jobs-crawler): source lifecycle, dispatch, retention
- [redirect service (service-files)](#redirect-service): /r/{slug} + Connect RPC
- [JobSnapshot wire format](#jobsnapshot-wire-format) — public R2 contract
- [NATS events](#nats-events) — internal pipeline subjects
- [OpenObserve streams](#openobserve-streams) — analytics + logs
- [Trustage workflows](#trustage-workflows) — scheduled cadences
- [Authentication](#authentication) — JWT + Hydra
- [Error codes](#error-codes) — HTTP + Connect mapping

---

## Services overview

| Service | Port | Public host | Image |
|---|---|---|---|
| stawi-jobs-api | `:8082` | `api.stawi.org/jobs` | `ghcr.io/antinvestor/stawi-jobs-api` |
| stawi-jobs-candidates | `:8080` | `api.stawi.org` | `ghcr.io/antinvestor/stawi-jobs-candidates` |
| stawi-jobs-crawler | `:8080` | cluster-internal | `ghcr.io/antinvestor/stawi-jobs-crawler` |
| redirect (service-files) | `:8080` | `r.stawi.org` | `ghcr.io/antinvestor/service-files-redirect` |
| jobs.stawi.org | — | CF Pages | static Hugo + Preact islands |

Public traffic enters through the Envoy Gateway on `api.stawi.org` (Connect-auth middleware attaches JWT claims). Internal service-to-service calls go through cluster DNS (`*.stawi-jobs.svc`). Admin endpoints are reachable only from inside the cluster.

---

## stawi-jobs-api

Base: `https://api.stawi.org/jobs`

Handles search, job detail, category listings, statistics, and the per-page-view beacon that triggers analytics + the per-job liveness probe (via the redirect service).

### Public search & discovery

All endpoints below are **unauthenticated** and **idempotent**. Responses are JSON with `Content-Type: application/json`.

#### `GET /search`

Full-text + facet search over canonical jobs. The workhorse.

Query parameters:

| Param | Type | Default | Notes |
|---|---|---|---|
| `q` | string | `""` | Free-text query. When empty, sort defaults to `recent`; when set, `relevance`. |
| `category` | string | `""` | Category slug (`programming`, `design`, `data`, etc.). |
| `remote_type` | string | `""` | `remote`, `hybrid`, `onsite`. |
| `employment_type` | string | `""` | `full_time`, `contract`, `part_time`. |
| `seniority` | string | `""` | `junior`, `mid`, `senior`, `staff`. |
| `country` | string | `""` | ISO 3166-1 alpha-2 (`KE`, `NG`, `US`). |
| `sort` | string | computed | `relevance` / `recent` / `quality` / `salary_high`. |
| `limit` | int | `20` | 1–100. |
| `cursor` | string | `""` | Opaque keyset cursor returned by the previous page. Empty-query pagination only. |
| `offset` | int | `0` | Text-search pagination. |

Response body:

```json
{
  "results": [
    {
      "id": 42,
      "slug": "senior-backend-engineer-at-acme-a1b2c3",
      "title": "Senior Backend Engineer",
      "company": "Acme",
      "location_text": "Remote — EU timezones",
      "country": "DE",
      "remote_type": "remote",
      "category": "programming",
      "salary_min": 80000,
      "salary_max": 110000,
      "currency": "EUR",
      "posted_at": "2026-04-10T08:00:00Z",
      "quality_score": 82.5,
      "snippet": "Build and operate payments-platform services handling...",
      "is_featured": true
    }
  ],
  "next_cursor": "MTcxMjM0NTY3ODkwfDQy",
  "has_more": true
}
```

`next_cursor` is present only when the server has another page. When `q` is non-empty, use `offset` instead of `cursor` for pagination.

Example:

```bash
curl "https://api.stawi.org/jobs/search?q=golang&remote_type=remote&limit=10&sort=relevance"
```

#### `GET /categories`

Category facets with counts.

```json
{
  "categories": [
    { "key": "programming", "count": 3481 },
    { "key": "design", "count": 612 },
    ...
  ]
}
```

#### `GET /sources`

Source health report (active, degraded, paused counts by type). Intended for dashboards, not end users.

```json
{
  "count": 63,
  "sources": [
    { "id": 12, "type": "greenhouse", "status": "active", "health_score": 0.95, ... }
  ]
}
```

#### `GET /jobs/top`

Top-ranked canonical jobs by quality score.

Query: `limit` (default 20, max 100), `min_score` (float, default 0).

#### `GET /jobs/{id}`

Fetch a single canonical job by integer ID. Returns the raw `CanonicalJob` row (all AI-extracted fields included). Prefer `/jobs/{slug}/view` for the browser path, which drives the frontend's `/r/{slug}` tracking.

#### `GET /stats`

Cluster-level counters.

```json
{
  "active_sources": 52,
  "active_jobs": 4129,
  "jobs_posted_today": 148,
  "countries_covered": 34
}
```

#### `GET /api/search`, `GET /api/categories`, `GET /api/categories/{slug}/jobs`, `GET /api/jobs/latest`, `GET /api/stats/summary`

Prefix variants intended for the frontend's React islands (they expect `/api/*` so the HTTPRoute can strip `/jobs` cleanly). Behaviour matches the unprefixed versions; same request params, same response shape.

### View ingest & attribution

#### `POST /jobs/{slug}/view`

The browser fires this via `navigator.sendBeacon` on `JobDetail` mount. It serves two purposes:

1. **Server-side attribution**: writes a `stawi_jobs_views` row to OpenObserve with `profile_id` pulled from the JWT, `ip_hash` (SHA-256 of CF-Connecting-IP), CF-IPCountry, User-Agent, Referer.
2. **No liveness probe**: destination URL liveness lives entirely in the redirect service now. This endpoint is pure analytics.

Request:
- Method: `POST`
- Path: `/jobs/{slug}/view`
- Body: empty (text/plain; sendBeacon-friendly)
- Auth: optional — send `Authorization: Bearer <jwt>` to attach `profile_id`; anonymous otherwise
- CORS: `Access-Control-Allow-Origin: *` (no credentials)

Response: `204 No Content`. Fire-and-forget; the browser doesn't wait.

`OPTIONS` preflight is answered with the same CORS envelope.

### Admin: republish & backfill

Gate: authenticated via the `SecurityManager` (Hydra JWT). When no authenticator is present in cluster config, endpoints are marked UNPROTECTED in logs.

#### `POST /admin/republish?status=active&limit=1000`

Re-emits publish for every canonical row matching `status` (default `active`). Detached goroutine — responds `202 Accepted` immediately, surveys progress via structured logs.

#### `POST /admin/backfill?from_id=&to_id=&since=&batch_size=&min_quality=&trigger_deploy=`

Publishes canonical jobs to R2 for Hugo site generation. Useful for initial site bootstrap or recovering from a wiped R2 bucket. All query params optional:

| Param | Type | Default | Notes |
|---|---|---|---|
| `from_id` | int64 | `0` | Starting canonical job ID. |
| `to_id` | int64 | `0` | End bound (0 = no cap). |
| `since` | RFC3339 | — | Alternative to IDs: only jobs posted after this. |
| `batch_size` | int | `500` | Rows per DB page. |
| `min_quality` | float | `50` | Quality floor. |
| `trigger_deploy` | bool | `false` | On completion, POST to the CF Pages deploy hook. |

Response `202 Accepted`, job runs asynchronously.

---

## stawi-jobs-candidates

Base: `https://api.stawi.org`

Owns candidate profiles, saved-job bookmarks, CV parsing + scoring, and the matching pipeline. Authenticated endpoints require a Hydra JWT with `sub` = profile ID.

### Authenticated profile

#### `GET /me`

Returns the candidate profile for the calling JWT.

Response: full `CandidateProfile` (see domain/candidate.go).

#### `GET /me/subscription`

Returns subscription tier, plan ID, and recent matches-sent counts.

```json
{
  "subscription": "paid",
  "plan_id": "pro",
  "matches_sent": 18,
  "last_matched_at": "2026-04-17T09:00:00Z"
}
```

#### `GET /candidates/profile` / `PUT /candidates/profile`

CRUD on the candidate's own profile. PUT accepts a JSON patch of any field listed in the `CandidateProfile` struct (skills, preferences, comm channels, etc.).

#### `POST /candidates/register`

**Unauthenticated** — accepts a multipart form with the CV file (`cv`), email, name, phone. Extracts text, runs `ExtractCV` via Groq to populate profile fields, uploads the CV to the Files service, creates the candidate row, and **kicks off a background CV Strength Report**.

Request: `multipart/form-data` with:
- `cv` (file): PDF, DOCX, or TXT
- `email` (string)
- `name` (string)
- `phone` (string, optional)

Response: `201 Created` with the new profile.

#### `POST /candidates/onboard`

Authenticated. Updates the profile with onboarding answers (target role, experience level, preferred regions, salary expectations, visa status) and re-runs CV scoring if the target role changed.

#### `GET /candidates/matches` / `POST /candidates/matches/{id}/view`

List and mark-as-viewed for the candidate's match queue.

### Saved jobs

#### `POST /jobs/{id}/save`

Bookmark a job. `{id}` is the canonical job's int64 primary key.

Response: `201 Created` on first save, `200 OK` on idempotent re-save.

#### `DELETE /jobs/{id}/save`

Un-bookmark.

#### `GET /saved-jobs`

List the caller's bookmarks ordered by `saved_at DESC`. Query `limit` (default 50, max 200).

```json
{
  "count": 7,
  "saved_jobs": [
    { "canonical_job_id": 42, "saved_at": "2026-04-12T14:30:00Z" },
    ...
  ]
}
```

### CV Strength Report

Two endpoints — one cached read, one force-rescore — plus an auto-trigger on every CV upload path.

#### `GET /candidates/cv/score`

Returns the cached report for the calling profile. Fast path — no LLM call. When the stored CV text hash differs from the last-scored hash, the handler fires a background rescore so the next request serves fresh data.

Response headers:
- `Content-Type: application/json`
- `X-CV-Report-Stale: true|false` — `true` means a background rescore was triggered

Response body: `CVStrengthReport` JSON (see below).

#### `POST /candidates/cv/score`

Forces a fresh scoring round. Used by the frontend after the user applies a fix and wants to see the score move. Blocks until done (~3-5 s on cluster Groq).

Response body: `CVStrengthReport` JSON.

#### `CVStrengthReport` shape

```json
{
  "overall_score": 72,
  "components": {
    "ats": 80,
    "keywords": 65,
    "impact": 55,
    "role_fit": 78,
    "clarity": 85
  },
  "target_role": "Senior Backend Engineer",
  "role_family": "programming",
  "priority_fixes": [
    {
      "id": "strengthen-bullets",
      "title": "Rewrite 5 experience bullets for measurable impact",
      "impact": "high",
      "category": "impact",
      "why": "Bullets that describe work (\"built backend services\") score lower than bullets that describe outcome (...)",
      "auto_applicable": false
    },
    {
      "id": "add-keywords",
      "title": "Add missing keywords: ci/cd, microservices, graphql",
      "impact": "medium",
      "category": "keywords",
      "why": "ATS filters match on exact keywords from the job family. (...)",
      "auto_applicable": true,
      "suggestions": ["ci/cd", "microservices", "graphql"]
    }
  ],
  "rewrites": [
    {
      "work_index": 0,
      "bullet_index": 2,
      "before": "Built backend services in Go",
      "after": "Built Go microservices handling {X requests/min} across {Y services}",
      "reason": "added strong verb + metric placeholders"
    }
  ],
  "generated_at": "2026-04-18T15:02:11Z",
  "cv_version": "a1b2c3d4e5f6"
}
```

**Scoring weights** (sum to 100):

| Dimension | Weight | What it measures |
|---|---|---|
| ATS | 20% | Missing standard sections, length sanity, table-layout penalty |
| Keywords | 20% | Hit rate against role-family canonical keyword list (+ synonyms) |
| Impact | 25% | Per-bullet: numbers, units, strong verbs, outcome language, weak-verb penalty |
| RoleFit | 20% | Cosine similarity of CV embedding vs family reference paragraph |
| Clarity | 15% | Buzzword density, passive voice, sentence length, "responsible for" penalty |

Auto-triggered on:
1. `POST /candidates/register` (after CV upload)
2. `POST /candidates/onboard` (after target role change)
3. `POST /webhooks/inbound-email` (after email-attached CV)

All three schedule a goroutine with 90s timeout so the user's response isn't blocked on LLM latency.

### Internal signals

#### `POST /internal/link-expired`

Called by the redirect service when a /r/{slug} link's destination probe fails. Request body:

```json
{
  "link_id": "01HZY7KPABCDEFGHJKLMNOPQRS",
  "slug": "xYz9abc",
  "affiliate_id": "canonical_job_42",
  "destination_url": "https://acme.example/careers/senior-backend",
  "expired_at": "2026-04-18T15:00:00Z"
}
```

Behaviour:
1. Parse `affiliate_id` — if it doesn't match `^canonical_job_(\d+)$` return `204 No Content` (forward-compatible for other domains).
2. `MarkExpiredIfActive(canonical_job_id)` — idempotent `WHERE status='active'` update.
3. On `rows_affected=1`, fan out `job_expired` notifications via the NotificationService to every profile that bookmarked the job.

No auth — cluster-internal. Gateway HTTPRoute doesn't expose `/internal/*`.

Response: `204 No Content` (or `4xx/5xx` on validation/processing errors).

### Webhooks

#### `POST /webhooks/inbound-email`

Ingest a CV emailed to the Stawi address. Expects a multipart form with the parsed email metadata + attachment. Creates or updates a CandidateProfile by email match, runs CV extraction + scoring.

#### `POST /webhooks/billing`

Payment status callback from `service-payment`. Body:

```json
{
  "profile_id": "01HY...",
  "status": "active|cancelled|expired|trial",
  "plan_id": "pro",
  "subscription_id": "sub_..."
}
```

Updates the candidate's `subscription` tier and `auto_apply` flag. Signed by service-payment's webhook PSK (verified upstream).

---

## stawi-jobs-crawler

Cluster-internal. Admin endpoints protected by the `SecurityManager` when configured; Trustage workflows call them over internal cluster DNS.

### Source lifecycle

#### `POST /admin/sources/pause?id=<N>`

Mark a source as `paused` — crawler skips it on the next sweep.

#### `POST /admin/sources/enable?id=<N>`

Return a paused/disabled source to `active`, reset `consecutive_failures=0`.

#### `GET /admin/sources/health`

Every source ordered by worst health first. Returns full `Source` rows.

### Crawl dispatch

#### `GET /admin/sources/due?limit=<N>`

Returns source IDs currently due for a crawl (`next_crawl_at <= NOW`). Used by Trustage's `source.crawl.dispatch` workflow to enumerate targets.

```json
{ "count": 27, "ids": [1, 5, 12, 14, ...] }
```

#### `POST /admin/crawl/dispatch`

Fire a `crawl.request` NATS message for a single source.

Body:
```json
{ "source_id": 42, "attempt": 1 }
```

Response `200 OK` with `{"ok": true, "source_id": 42}`. The crawler's NATS consumer picks up the message and runs `processSource`.

#### `POST /admin/crawl/dispatch-due?limit=<N>`

Convenience: enumerate + dispatch in one call. Used by the simple `source.crawl.sweep` Trustage workflow.

```json
{ "ok": true, "considered": 27, "dispatched": 27 }
```

### Retention

#### `POST /admin/retention/expire`

Flip canonical jobs past `expires_at` from `active` to `expired`. Idempotent — the SQL is a conditional UPDATE.

#### `POST /admin/retention/mv-refresh`

`REFRESH MATERIALIZED VIEW CONCURRENTLY mv_job_facets`. Postgres serialises concurrent REFRESH calls so duplicate fires are safe.

#### `POST /admin/retention/stage2`

Physically deletes R2 snapshots for canonical rows whose expired-grace window has passed. Batches 1000 rows at a time. Runs synchronously — fine for the nightly cadence.

### Recovery

#### `POST /admin/rebuild-canonicals`

Truncates canonical tables and rebuilds them by re-running every variant through the dedupe engine. Useful for recovering from dedupe bugs that produced inflated counts. Destructive — use with care.

---

## redirect service

Base: `https://r.stawi.org`

The only public path is `/r/{slug}`; everything else is authenticated Connect RPC under `/redirect.v1.RedirectService/*`.

### `GET /r/{slug}`

Resolve a tracked link. Happy path:

1. Look up the Link by slug (cache-first, then DB).
2. If `State != ACTIVE` → render the **dead-link page** (410 Gone with `X-Robots-Tag: noindex, nofollow`).
3. Record the Click asynchronously (batched to DB, emits `stawi_jobs_applies` event to OpenObserve).
4. Fire a throttled destination-URL probe (background goroutine, 15-min throttle per link).
5. 302 redirect to the destination URL with `Cache-Control: no-store`.

On repeated probe failures (404/410 terminal, or 3 consecutive 5xx), the handler:
- Flips `LinkState = EXPIRED`.
- Invalidates the link cache so the next hit sees the dead-link page.
- POSTs to every URL in `LINK_EXPIRED_WEBHOOKS` (stawi-jobs-candidates consumes this).
- Emits `redirect_probe` event to `stawi_jobs_events` in OpenObserve.

### Connect RPC: `redirect.v1.RedirectService`

Authenticated via OIDC interceptors — only internal service accounts.

| RPC | Purpose |
|---|---|
| `CreateLink` | Mint a new tracked link. stawi-jobs-crawler calls this at publish time with `affiliate_id = "canonical_job_<id>"`. |
| `GetLink` | Fetch link metadata by ID. |
| `UpdateLink` | Change state (ACTIVE / PAUSED / EXPIRED / DELETED), destination URL, or metadata. |
| `DeleteLink` | Soft-delete; historical clicks remain queryable. |
| `ListLinks` | Search by affiliate_id, campaign, or state. Streaming response. |
| `GetLinkStats` | Aggregate click counts: total, unique, by country/device/browser, per-day histogram. |
| `ListClicks` | Click-level detail for an affiliate. Streaming response. |

Full proto: `service-files/proto/redirect/redirect/v1/redirect.proto`.

---

## JobSnapshot wire format

The public contract between the backend and the frontend. Served from R2 at:

```
https://jobs-repo.stawi.org/jobs/{slug}.json            # source language
https://jobs-repo.stawi.org/jobs/{slug}.{lang}.json     # translated variants
```

Supported language codes: `en`, `es`, `fr`, `de`, `pt`, `ja`, `ar`, `zh`.

```json
{
  "schema_version": 1,
  "id": 42,
  "slug": "senior-backend-engineer-at-acme-a1b2c3",
  "title": "Senior Backend Engineer",
  "company": {
    "name": "Acme Corp",
    "slug": "acme-corp",
    "logo_url": "https://...",
    "verified": true
  },
  "category": "programming",
  "location": {
    "text": "Berlin, Germany",
    "remote_type": "hybrid",
    "country": "DE"
  },
  "employment": {
    "type": "full_time",
    "seniority": "senior"
  },
  "compensation": {
    "min": 80000,
    "max": 110000,
    "currency": "EUR",
    "period": "year"
  },
  "skills": {
    "required": ["Go", "PostgreSQL", "Kubernetes"],
    "nice_to_have": ["GraphQL", "Rust"]
  },
  "description_html": "<p>Sanitised HTML description...</p>",
  "apply_url": "https://r.stawi.org/r/xYz9abc",
  "posted_at": "2026-04-10T08:00:00Z",
  "updated_at": "2026-04-18T12:00:00Z",
  "expires_at": "2026-08-08T08:00:00Z",
  "quality_score": 82.5,
  "is_featured": true,
  "language": "en"
}
```

Frontend fetcher: `ui/app/src/api/snapshot.ts`. The `fetchSnapshot(slug, lang)` helper tries `{slug}.{lang}.json` first, falls back to `{slug}.json` on 404.

---

## NATS events

JetStream subjects the internal pipeline uses. All payloads are JSON.

### Stream `svc_stawi_jobs_events`

Subject: `svc.stawi-jobs.events.>`. Retention: workqueue (exactly-once). MaxAge: 24h.

| Event | Payload | Emitter → Consumer |
|---|---|---|
| `crawl.request` | `{source_id, attempt}` | api admin (Trustage) → crawler |
| `variant.raw.stored` | `{variant_id, source_id}` | crawler fetch → dedup |
| `variant.deduped` | `{variant_id, source_id}` | dedup → normalize |
| `variant.normalized` | `{variant_id, source_id}` | normalize → validate |
| `variant.validated` | `{variant_id, source_id}` | validate → canonical |
| `source.urls.discovered` | `{source_id, urls[]}` | normalize (HTML sources) → source-expansion |
| `source.quality.review` | `{source_id}` | source-quality → admin alerts |
| `job.ready` | `{canonical_job_id}` | canonical → publish |
| `job.published` | `{canonical_job_id, slug, source_lang, r2_version}` | publish → translate |

### Stream `svc_stawi_jobs_candidates`

Subject: `svc.stawi-jobs.candidates.>`. Retention: workqueue. MaxAge: 24h.

| Event | Payload | Purpose |
|---|---|---|
| `candidate.profile.created` | `{candidate_id}` | Fan-out trigger after registration. |
| `candidate.embedding` | `{candidate_id, text}` | Async embedding generation for matching. |

### Stream `svc_files_redirect` (service-files)

Subject: `svc.files-redirect.>`. Retention: workqueue. MaxAge: 1h.

Currently used only for internal redirect-service queue work.

---

## OpenObserve streams

Writes go through the shared `ANALYTICS_*` credentials (Vault: `antinvestor/stawi-jobs/common/analytics-credentials`). Ingest endpoint inside the cluster: `http://openobserve-openobserve-standalone.telemetry.svc:5080/api/default/<stream>/_json`.

### `stawi_jobs_views`

Every page-view beacon + every inactive-link hit. Fields:

```
_timestamp       microseconds (auto-stamped)
event            "server_view" (api beacon) | "link_inactive" (redirect)
slug             canonical slug
profile_id       JWT sub (optional, blank for anon)
ip_hash          sha256(CF-Connecting-IP)[:12]
user_agent       raw UA
referer          raw Referer
cf_country       CF-IPCountry
cf_ray           CF-Ray
```

### `stawi_jobs_applies`

Every `/r/{slug}` redirect and every click on an inactive link. Fields:

```
event                "redirect" | "link_inactive"
link_id              redirect-service link UUID
affiliate_id         "canonical_job_<id>" for stawi-jobs
slug                 redirect slug
campaign / source / medium
ip_address           raw IP (internal, not hashed)
user_agent / referer
cf_country / cf_ray
```

### `stawi_jobs_events`

Internal pipeline telemetry:

- `liveness_probe` — destination-URL reachability outcomes from the redirect service (fields: `link_id`, `affiliate_id`, `probe_status`, `probe_error`, `reachable`, `consecutive_failures`, `link_expired`).
- `translate_result` — per-language translation outcomes (coming in the translate handler).

### RUM stream

Written directly from the browser via the `@openobserve/browser-rum` + `@openobserve/browser-logs` SDKs. Token-authenticated from `hugo.toml` `rumClientToken`. Events:

- `job_view`: on JobDetail mount. Carries `canonical_job_id`, `slug`, `category`, `company`, `country`, `ui_language`, `snapshot_language`, `translated_notice_shown`, `referrer`.
- `job_view_engaged`: at 10s dwell. Carries `dwell_ms`, `scroll_depth_pct`.
- `apply_click`: on apply anchor click. Carries `apply_url`, `dwell_ms`.
- Auto: page views, resource timings, long tasks, errors, session replay.

`setAnalyticsUser({id, name, email})` attaches the authenticated profile to the session; `setAnalyticsContext(key, value)` stamps a session-wide field.

---

## Trustage workflows

Workflow definitions in `definitions/trustage/*.json`. Each JSON file carries a top-level `schedule` block consumed by Trustage's cron scheduler (pending Trustage schedule-seeding support).

| File | Cron | Calls |
|---|---|---|
| `source-crawl-sweep.json` | `1m` | `POST /admin/crawl/dispatch-due` |
| `retention-expire.json` | `15m` | `POST /admin/retention/expire` |
| `retention-mv-refresh.json` | `5m` | `POST /admin/retention/mv-refresh` |
| `retention-stage2.json` | `24h` | `POST /admin/retention/stage2` |

See `definitions/trustage/README.md` for the per-source-vs-sweep design trade-off and the dispatch-due vs enumeration patterns.

---

## Authentication

### JWT from Ory Hydra

Every authenticated endpoint expects an RS256-signed JWT in `Authorization: Bearer <token>`. Tokens come from Hydra at `https://oauth2.stawi.org`:

- Discovery: `https://oauth2.stawi.org/.well-known/openid-configuration`
- JWKS: `https://oauth2.stawi.org/.well-known/jwks.json`
- Issuer: `https://oauth2.stawi.org`

Key claims:
- `sub`: the profile ID (matches `profile_profiles.id` in the profile service)
- `aud`: `stawi_jobs_candidates` or `stawi_jobs_crawler` depending on service
- `iss`: `https://oauth2.stawi.org`
- `exp`: expiry

Verification happens once at the gateway / service SecurityManager; downstream handlers use `security.ClaimsFromContext(ctx).GetProfileID()` to read the authenticated identity.

### Anonymous endpoints

The following don't require auth:

- `GET /search`, `/categories`, `/jobs/top`, `/jobs/{id}`, `/stats`, `/api/*`
- `POST /jobs/{slug}/view` (anonymous attribution is supported; JWT optional)
- `POST /candidates/register` (onboarding)
- `POST /webhooks/*` (gateway enforces PSK)
- `GET /r/{slug}` (the whole point of redirect service)
- `GET /healthz` (every service)

### Internal endpoints

`/admin/*` and `/internal/*` paths are gated by internal cluster network policy — the Envoy Gateway HTTPRoute for `api.stawi.org` doesn't include them. Trustage workflows and other in-cluster services call them directly on `http://<service>.<namespace>.svc`.

---

## Error codes

### HTTP

Standard HTTP semantics across all endpoints:

| Code | When |
|---|---|
| `200 OK` | Success with body. |
| `201 Created` | New resource created. |
| `202 Accepted` | Accepted for async processing. |
| `204 No Content` | Success, no body (view beacon, link-expired signal, delete). |
| `400 Bad Request` | Input validation failed. Body has `{"error": "..."}`. |
| `401 Unauthorized` | Missing or invalid JWT. |
| `403 Forbidden` | Authenticated but not permitted. |
| `404 Not Found` | Resource doesn't exist (or not `status='active'`). |
| `410 Gone` | The dead-link page from redirect service. |
| `500 Internal Server Error` | Unexpected, includes message in body. |
| `503 Service Unavailable` | Downstream dependency (R2, events bus) isn't configured. |

### Connect RPC

Services expose Connect errors (mapped from gRPC codes):

| Code | When |
|---|---|
| `CodeInvalidArgument` | Validation failure. |
| `CodeUnauthenticated` | Missing/invalid JWT. |
| `CodePermissionDenied` | Authenticated but not authorized. |
| `CodeNotFound` | Resource missing. |
| `CodeAlreadyExists` | Creating a duplicate. |
| `CodeInternal` | Unexpected server error. |

---

## Client SDKs

Go clients live in `stawi.jobs/pkg/services/`:

- `services.RedirectClient` — `CreateLink`, `GetLink`, `UpdateLink`, `ExpireLink`.
- `services.CreateTrackedLink(ctx, client, destinationURL, candidateID, campaign)` — thin wrapper for the apply-URL flow.
- `services.NewClients(ctx, cfg, ...)` — bundles Notification, Files, Redirect, Payment, Profile clients with OAuth2-backed Connect interceptors.

TypeScript clients in `ui/app/src/api/`:

- `fetchSnapshot(slug, lang?)` — per-language R2 snapshot fetch with 404 fallback.
- `pingJobView(slug)` — sendBeacon wrapper for the view endpoint.
- `search(...)`, `listCategories()`, `listJobsByCategory()`, `fetchTopJobs()`, `fetchStats()` — public search API.
- `saveJob(jobId)`, `unsaveJob(jobId)`, `listSavedJobs()` — bookmarks.
- `fetchCVScore()`, `rescoreCV()` — CV Strength Report.
- `loginWithOIDC()`, `logout()` — auth via the shared `@stawi/auth-runtime`.

---

## Rate limits & operational notes

### Rate limits

- None enforced at the service layer today.
- Cloudflare in front of `api.stawi.org` applies global DDoS protection + bot fight mode.
- The crawler is self-rate-limited: each source has a `CrawlIntervalSec` setting and the circuit breaker (`ConsecutiveFailures` → backoff) prevents hammering flaky sources.
- Liveness probes are throttled to once per link per 15 minutes.
- Translation fan-out is gated by `TRANSLATE_MIN_QUALITY` (default 70) and `TRANSLATE_ENABLED` (default false).

### Observability

- Every request: structured logs via `util.Log(ctx)` with fields. Pipe to OpenObserve for SQL queries.
- Traces: OpenTelemetry auto-instrumentation on every Connect handler + pipeline stage. View in `observe.stawi.org`.
- Metrics: Frame's built-in counters + pipeline stage durations + RUM web vitals from the browser.

### Rollout

- Images auto-tag on `v*.*.*` git tags via the release workflow.
- Flux image-automation watches the repo for new semver tags and updates the HelmRelease image fields.
- Schema migrations run via the migration container on helm chart upgrade.
- Zero-downtime rolling updates across 3 crawler replicas + N candidates/api replicas.

---

## Quickstart examples

### Find remote Go jobs

```bash
curl 'https://api.stawi.org/jobs/search?q=golang&remote_type=remote&limit=5' | jq '.results[]|{slug,company:.company}'
```

### Bookmark a job (authenticated)

```bash
TOKEN=$(ory token...)  # obtain OIDC access token
curl -X POST -H "Authorization: Bearer $TOKEN" 'https://api.stawi.org/jobs/42/save'
```

### Fetch your CV Strength Report

```bash
curl -H "Authorization: Bearer $TOKEN" 'https://api.stawi.org/candidates/cv/score' | jq .overall_score
```

### Fire a manual crawl dispatch (cluster-internal)

```bash
kubectl run curl -n stawi-jobs --image=curlimages/curl --rm -it --restart=Never -- \
  curl -fsS -XPOST 'http://stawi-jobs-crawler.stawi-jobs.svc/admin/crawl/dispatch-due'
# → {"ok":true,"considered":27,"dispatched":27}
```

### Emit a test CV view beacon

```js
navigator.sendBeacon(
  'https://api.stawi.org/jobs/senior-backend-engineer-at-acme-a1b2c3/view',
  new Blob([''], { type: 'text/plain' })
);
```

---

## Changelog

| Version | Date | Highlights |
|---|---|---|
| v4.3.5 | 2026-04-18 | Structured logging across bootstraps, regression test suite expansion |
| v4.3.4 | 2026-04-18 | Trustage-driven dispatch, CV Strength Report, multilingual + RUM + redirect webhooks |
| v4.3.3 | 2026-04-17 | OIDC migration to `@stawi/auth-runtime`, UX polish |
| v4.3.2 | 2026-04-17 | Frontend audit fixes |
| v4.3.1 | 2026-04-17 | Hugo PostCSS pipeline + R2 deploy hook |

Full history: `git log v4.3.5 --format='%h %s'`.
