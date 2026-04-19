# Scaling Hugo to Millions of Jobs — Design

## Overview

Scale the public jobs surface at `jobs.stawi.org` to handle ~20M lifetime jobs with a 4-month retention window (≈3–6M live at steady state), while preserving near-real-time freshness, live searchability, and operational simplicity.

The strategy is a strict separation between:

- **The Hugo app shell** — a tiny static site built only on code changes. Home, `/search`, `/jobs/[slug]`, `/categories/[slug]`, `/auth`, `/dashboard`, `/onboarding`. No per-job content, no content adapters, no sharded builds.
- **The content tier in R2** — one `jobs/<slug>.json` snapshot per job, fronted by Cloudflare's CDN at a public subdomain `job-repo.stawi.org`. Written directly by the publish handler. Reach millions of objects without ever involving Hugo.
- **The search tier in Postgres** — `tsvector` GIN indexes for full-text, partial indexes for the hot path, a 5-minute materialized view for facet counts. No second search system.

The design deliberately avoids: sharded Hugo builds, content adapters against remote data, Workers on the read path, OpenSearch, Typesense, or any service not already running in the cluster.

## Non-goals

- Per-job SEO is not a priority. Pages are client-rendered from R2; bots without JS see a skeleton. This is an explicit business tradeoff.
- No per-user personalization at the snapshot layer. User-state (saved/applied/matched) loads separately from the candidates API only for signed-in users.
- No live filtered facet counts. Facet counts shown on `/search` and `/categories/*` are **global over active jobs**, refreshed every 5 min.
- No Hugo taxonomies. Category pages are API-driven.

## Architecture

```
┌────────────────────────────────────────────────────────────────┐
│  CRAWL PIPELINE (existing)                                     │
│    crawl → dedup → normalize(AI) → validate(AI) → canonical    │
│                                                    → job.ready │
└────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌────────────────────────────────────────────────────────────────┐
│  PUBLISH HANDLER (single writer — transactional)               │
│    1. UPDATE jobs.search_tsv + status/r2_version (Postgres)    │
│    2. PUT   job-repo.stawi.org/jobs/<slug>.json (R2 public)    │
│    3. (best-effort) CF cache purge for that URL                │
│    4. emit publish.completed metric                            │
└────────────────────────────────────────────────────────────────┘

┌─────────────────────────────┐     ┌──────────────────────────────┐
│  APP SHELL (Hugo, CF Pages) │     │  SEARCH-API (Go, existing)   │
│    /, /search, /jobs/[slug] │     │   reads Postgres FTS         │
│    /categories/*, /auth/*   │     │   GIN(tsvector) + partials   │
│    <2K files, rebuilt on    │     │   facet counts from          │
│    code changes only        │     │   mv_job_facets (5-min MV)   │
└───────────┬─────────────────┘     └──────────────────────────────┘
            │                                   ▲
            ▼                                   │  JSON
   /jobs/<slug>/ → shell HTML                   │
            │                                   │
            ▼                                   │
   JS fetches job-repo.stawi.org/               │
        jobs/<slug>.json  ── R2 + CF CDN        │
            │                                   │
            ▼                                   │
   renders metadata + description               │
            │                                   │
            └── if signed in: fetch user-state ─┘
                from candidates API
```

**Key invariants:**

- Hugo never sees a job file. `ui/content/jobs/` is deleted.
- R2 is the only authoritative public read path for a single job.
- Postgres is source of truth *and* the search backend.
- Publish handler is the single writer that keeps PG row, R2 snapshot, and CF cache aligned.
- Facet counts on `/search` and `/categories/*` accept up to 5-min staleness; result rows and `/jobs/<slug>` are near-real-time (≤ 5 min edge TTL).

## R2 layout and snapshot schema

Single public R2 bucket `stawi-jobs-content`, custom domain `job-repo.stawi.org`, CORS allowing `https://jobs.stawi.org` and local dev origins.

```
job-repo.stawi.org/
├── jobs/
│   └── <slug>.json                 # one per job (the snapshot)
├── categories/
│   ├── index.json                  # [{slug,name,job_count,...}]
│   └── <cat-slug>.json             # category metadata (no job list)
├── companies/
│   ├── <company-slug>.json         # company profile (optional)
│   └── <company-slug>/logo.<ext>
└── stats/
    └── summary.json                # totals shown on home page
```

Design choices:

- Flat `jobs/` prefix. R2 has no hotspot concern for reads; we address by known slug and never LIST.
- No per-job lists inside `categories/<cat>.json`. Those lists come from `search-api`.
- No sitemap tree. SEO-per-job is out of scope for v1.
- `companies/<slug>/logo.*` is optional scope, added when the canonical pipeline reliably populates company metadata.

**HTTP headers for `jobs/<slug>.json`:**

```
Content-Type: application/json; charset=utf-8
Cache-Control: public, max-age=60, s-maxage=300
```

60s browser fresh, 5 min edge fresh, then revalidate.

**`JobSnapshot` schema (v1):**

```json
{
  "schema_version": 1,
  "id": 12345,
  "slug": "senior-go-developer-at-acme-corp-a3f7b2",
  "title": "Senior Go Developer",
  "company": {
    "name": "Acme Corp",
    "slug": "acme-corp",
    "logo_url": "https://job-repo.stawi.org/companies/acme-corp/logo.png",
    "verified": true
  },
  "category": "engineering",
  "location": {
    "text": "Lagos, Nigeria (Remote)",
    "remote_type": "remote",
    "country": "NG"
  },
  "employment": { "type": "full_time", "seniority": "senior" },
  "compensation": {
    "min": 80000, "max": 120000,
    "currency": "USD", "period": "year"
  },
  "skills": {
    "required": ["Go", "PostgreSQL"],
    "nice_to_have": ["Kubernetes"]
  },
  "description_html": "<p>…pre-rendered from markdown at publish time…</p>",
  "apply_url": "https://acme.com/careers/senior-go",
  "posted_at": "2026-04-15T10:00:00Z",
  "updated_at": "2026-04-17T08:00:00Z",
  "expires_at": "2026-08-15T10:00:00Z",
  "quality_score": 85,
  "is_featured": true,
  "source": { "name": "greenhouse", "host": "boards.greenhouse.io" }
}
```

- `description_html` is pre-rendered at publish time (markdown → sanitized HTML). The browser never parses markdown.
- `schema_version` lets us evolve the payload safely. Breaking changes bump the version and trigger a full re-render pass.
- Typical snapshot is 3–8 KB gzipped. 6M jobs × ~5 KB ≈ 30 GB in R2.
- No per-user fields. User-state is fetched separately behind auth.
- `expires_at` drives the client-side expired banner; physical deletion is handled by a separate retention job.

## Publish handler

Triggered by `job.ready` events from `CanonicalHandler`. Serialized per `canonical_id`. Deterministic, idempotent, retry-safe.

```go
func (h *PublishHandler) Handle(ctx context.Context, evt JobReadyEvent) error {
    job, err := h.repo.GetCanonical(ctx, evt.CanonicalID)
    if err != nil { return err }
    if job.Status == StatusDeleted { return h.unpublish(ctx, job) }

    snap := BuildSnapshot(job)                         // pure function
    body, _ := json.Marshal(snap)

    // Step 1 — R2 (idempotent PUT, overwrites by key)
    if err := h.r2.Put(ctx, "jobs/"+job.Slug+".json", body, publicHeaders); err != nil {
        return fmt.Errorf("r2 put: %w", err)
    }

    // Step 2 — Postgres: bump r2_version, set published_at; search_tsv auto-updates
    if err := h.repo.MarkPublished(ctx, job.ID, job.Version+1); err != nil {
        return fmt.Errorf("pg mark: %w", err)
    }

    // Step 3 — CF cache purge (best-effort, non-fatal)
    if err := h.cdn.PurgeURL(ctx, publicURL(job.Slug)); err != nil {
        h.log.Warn(ctx, "cache purge failed", "slug", job.Slug, "err", err)
    }

    h.metrics.IncPublished(job.Category)
    return nil
}
```

**Ordering rationale:** R2 first, then Postgres. If R2 fails, the row stays unpublished — `/search` won't surface a dead link. If PG fails after R2 succeeds, next retry is free; re-PUT is idempotent. If purge fails, edge serves stale up to `s-maxage`; tolerable.

**Postgres schema additions (on `jobs`):**

| Column | Type | Purpose |
|---|---|---|
| `published_at` | `timestamptz` NULL | Non-null ⇒ visible + has R2 snapshot |
| `r2_version`   | `int` NOT NULL default 0 | Monotonic per-slug version; bumped every publish |
| `search_tsv`   | `tsvector` GENERATED STORED | Weighted: title=A, company=B, skills=C, description=D |
| `status`       | `text` NOT NULL | `active` / `expired` / `deleted` |
| `expires_at`   | `timestamptz` | 4-month TTL from `posted_at` |

Partial indexes:

```sql
CREATE INDEX jobs_fts      ON jobs USING gin(search_tsv)                WHERE status='active';
CREATE INDEX jobs_recent   ON jobs(posted_at DESC, id DESC)             WHERE status='active';
CREATE INDEX jobs_category ON jobs(category, posted_at DESC, id DESC)   WHERE status='active';
CREATE INDEX jobs_skills   ON jobs USING gin(skills)                    WHERE status='active';
```

**Unpublish path** (soft-delete or expiry past grace):

```go
func (h *PublishHandler) unpublish(ctx context.Context, job *CanonicalJob) error {
    _ = h.r2.Delete(ctx, "jobs/"+job.Slug+".json")
    _ = h.repo.ClearPublished(ctx, job.ID)
    _ = h.cdn.PurgeURL(ctx, publicURL(job.Slug))
    return nil
}
```

**Bulk re-publish.** Admin endpoint `POST /admin/republish?status=active&limit=N`. Iterates active jobs in batches and emits one `job.ready` event per row. Used for schema bumps, template changes, or CSS refreshes that touch `description_html`. At 200/s, re-publishing 6M snapshots takes ~8h.

**Failure modes:**

| Failure | User impact | Recovery |
|---|---|---|
| R2 PUT fails | Job stays unsearchable | Pipeline retries with backoff |
| PG mark fails | Orphan snapshot in R2 briefly | Retry; idempotent |
| Cache purge fails | Edge stale up to s-maxage | Accept; optional retry queue |
| Poison event | Single slug fails repeatedly | DLQ after N retries; alert with slug |

## Hugo app shell

Small static site rebuilt only on code changes. Everything dynamic is fetched client-side.

```
ui/ (Hugo project)
├── layouts/           templates only
├── assets/            Tailwind source, JS modules
├── static/            images, fonts, favicon, _redirects
├── data/              truly-static nav config, locales
├── content/           ← shrinks dramatically
│   ├── _index.md          home
│   ├── search.md          /search (shell, no data)
│   ├── categories.md      /categories (lists categories)
│   ├── jobs.md            /jobs/ — single route for ALL slugs via rewrite
│   ├── about.md, pricing.md, privacy.md, terms.md
│   ├── auth/              login/callback flow
│   ├── dashboard/         signed-in shell (candidate)
│   └── onboarding/        candidate flow
└── hugo.toml
```

**Deletions:**

- Remove `ui/content/jobs/*.md` except the route shell.
- Remove `ui/scripts/sync-r2.sh` and its CF Pages build hook.
- Drop the `[taxonomies] category` block from `hugo.toml`.
- Remove `ui/data/stats.json` and `ui/data/categories.json` build-time fetches.

**Dynamic routing (CF Pages rewrite, not Hugo-level).** Add `ui/static/_redirects`:

```
/jobs/*        /jobs/        200
/categories/*  /categories/  200
/companies/*   /companies/   200
```

`200` is a rewrite: the URL in the bar stays, the served HTML is the shell for that route. Client JS reads `location.pathname`, extracts the slug, fetches from R2 or API.

**Client runtime** under `ui/assets/js/`:

```
main.js                 app bootstrap, route dispatcher
api.js                  fetch wrappers + auth header handling
routes/
  ├── job-detail.js     reads slug, fetches R2, renders
  ├── search.js         form → search-api, paginate results
  ├── category.js       reads slug, fetches R2 + API list
  └── dashboard.js      signed-in candidate view
components/
  ├── user-state.js     saved/applied/match widget
  ├── skeleton.js       loading placeholders
  └── toast.js
auth.js                 OIDC handoff via @stawi/auth-runtime
```

Vanilla JS + Tailwind. Route-specific code loaded via dynamic `import()`. ~20 KB gzipped bundle is enough.

**`/jobs/<slug>/` render flow:**

1. Hugo shell HTML served (CDN-cached).
2. `job-detail.js` extracts slug from `location.pathname`.
3. `fetch('https://job-repo.stawi.org/jobs/' + slug + '.json')`.
4. On 200: hydrate DOM; update `<title>` and canonical link.
5. On 404: render "expired or removed" state.
6. If signed in: parallel `fetch('/api/candidates/jobs/' + slug + '/state')` for the user-state widget.

**`/search` render flow:**

1. Shell HTML with form + empty results container.
2. `search.js` reads `location.search` query string.
3. `fetch('/api/search?q=…')`.
4. Render denormalized results. Facet counts rendered from MV-backed payload.

**`/categories/<slug>/` render flow:**

1. `category.js` extracts slug.
2. Parallel: `GET job-repo.stawi.org/categories/<slug>.json` (hero metadata) + `GET /api/categories/<slug>/jobs` (live list).
3. Render as each arrives.

**Build lifecycle:**

- Source: `main` branch.
- Build command: `hugo --minify`. No R2 sync, no data fetches, no adapters.
- Output: a few hundred files, ~2 MB. Far under CF Pages' 20K-file limit.
- Build time target: <30s.
- Every PR gets a preview URL from Pages.
- Shell HTML cached 5 min at the edge. Content-hashed JS/CSS cached `immutable, max-age=31536000`.

**CORS on `job-repo.stawi.org`:**

```json
[{
  "AllowedOrigins": ["https://jobs.stawi.org", "http://localhost:1313", "http://localhost:8082"],
  "AllowedMethods": ["GET", "HEAD"],
  "AllowedHeaders": ["*"],
  "MaxAgeSeconds": 86400
}]
```

No credentials, no cookies.

**What the shell never does:**

- Never LISTs R2. Always goes through `search-api`.
- Never parses markdown (pre-rendered at publish).
- Never falls back to the API on an R2 miss. 404 is authoritative.

## Search and browse

**Endpoints on the existing `search-api` service.**

| Endpoint | Purpose | Edge cache |
|---|---|---|
| `GET /api/search?q=…&category=…&remote=…&sort=…&cursor=…` | main search | none |
| `GET /api/categories` | list categories with counts | 60s |
| `GET /api/categories/<slug>/jobs?cursor=…` | category feed | 30s |
| `GET /api/jobs/latest?limit=20` | home feed / featured | 30s |
| `GET /api/stats/summary` | home counters | 300s |

All responses denormalized enough for list rendering — no R2 follow-up needed.

**Text search query:**

```sql
SELECT slug, title, company, ...
FROM jobs
WHERE status = 'active'
  AND search_tsv @@ plainto_tsquery('simple', $1)
  AND ($2::text IS NULL OR category    = $2)
  AND ($3::text IS NULL OR remote_type = $3)
ORDER BY ts_rank_cd(search_tsv, plainto_tsquery('simple', $1)) DESC,
         posted_at DESC, id DESC
LIMIT 21 OFFSET $4;
```

Uses `simple` config to stay language-agnostic.

**Recency query (keyset pagination):**

```sql
SELECT slug, title, ...
FROM jobs
WHERE status = 'active'
  AND ($1::text IS NULL OR category = $1)
  AND (posted_at, id) < ($2::timestamptz, $3::bigint)
ORDER BY posted_at DESC, id DESC
LIMIT 21;
```

**Pagination policy:**

- Empty-query browse uses **keyset**; any depth is cheap.
- Text search uses **OFFSET, capped at 50 pages × 20 = 1,000 results**. Beyond that the UI shows "refine your search."
- Always fetch `pageSize + 1`; derive `has_more` from the overflow row.

**Facet MV:**

```sql
CREATE MATERIALIZED VIEW mv_job_facets AS
SELECT 'category'::text AS dim, category AS key, count(*) AS n
  FROM jobs WHERE status='active' GROUP BY 1,2
UNION ALL
SELECT 'remote_type',     remote_type,     count(*) FROM jobs WHERE status='active' GROUP BY 1,2
UNION ALL
SELECT 'employment_type', employment_type, count(*) FROM jobs WHERE status='active' GROUP BY 1,2
UNION ALL
SELECT 'seniority',       seniority,       count(*) FROM jobs WHERE status='active' GROUP BY 1,2
UNION ALL
SELECT 'country',         country,         count(*) FROM jobs WHERE status='active' GROUP BY 1,2;

CREATE UNIQUE INDEX ON mv_job_facets(dim, key);
```

`REFRESH MATERIALIZED VIEW CONCURRENTLY mv_job_facets` every 5 min by `scheduler`. Facet counts are **global over active jobs**, not filtered by the current query — deliberate simplification.

**Response shape (search):**

```json
{
  "query": "go developer",
  "total_hint": 8423,
  "results": [ /* 20 denormalized rows */ ],
  "cursor_next": "…",
  "has_more": true,
  "facets": {
    "category":        [{"key":"engineering","count":14203}, …],
    "remote_type":     [{"key":"remote","count":31200}, …],
    "employment_type": […],
    "seniority":       […],
    "country":         […]
  },
  "sort": "relevance",
  "page": 1
}
```

`total_hint` is exact if the predicate hits ≤10K rows, otherwise an estimate or `null` with `has_more`.

**Sort modes:** `relevance` (default with `q`), `recent` (default without), `quality`, `salary_high`. Keyset works for all except `relevance`, which uses capped OFFSET.

**Performance targets:**

| Query | p99 |
|---|---|
| FTS w/ filter | < 50ms |
| Recency feed | < 10ms |
| Category feed | < 10ms |
| Facet MV read | < 5ms |
| Home stats | < 5ms |

**Rate limiting:** per-IP limit on `/api/search` at the CF edge (e.g., 60 req/min).

**Deliberately not yet:** autocomplete (`pg_trgm` later if needed), "more like this" (`pgvector` later), per-user ranking.

## Cache invalidation and retention

### Freshness model

At ~200 publishes/sec sustained, per-URL cache purge is not viable on non-Enterprise Cloudflare plans. Chosen strategy: short edge TTL, no per-publish purge on the hot path.

```
Cache-Control: public, max-age=60, s-maxage=300
```

Updates propagate within ~5 min at the outside. Newly-posted jobs appear instantly in search (no edge cache on search-api). The 5-min window only affects mid-life edits to existing jobs.

Upgrade paths if tighter freshness is ever needed:

- Lower `s-maxage` to 30–60s (trade more edge misses for freshness).
- Move to Worker-proxied R2 and use surrogate-key tag purges (requires changing the read-path architecture).

### Bulk purge lane

For rare operations that update all snapshots (schema bump, CSS change affecting `description_html`):

1. Run bulk re-publish (~8h for 6M objects).
2. Wait `s-maxage` for natural edge expiry.
3. Optionally call CF "Purge Everything" once on the `job-repo.stawi.org` zone.

`job-repo.stawi.org` should be its own CF zone so a purge-everything doesn't wipe the app shell cache on `jobs.stawi.org`.

### Retention — 4-month TTL

**Stage 1 — expire (every 15 min, existing `scheduler`):**

```sql
UPDATE jobs SET status='expired', published_at=NULL
WHERE status='active' AND expires_at < now();
```

Row leaves all partial indexes automatically. R2 snapshot **kept** for 7 days of grace. Client shows "no longer accepting applications" banner.

**Stage 2 — physical delete (nightly):**

```sql
SELECT slug FROM jobs
WHERE status='expired' AND expires_at < now() - interval '7 days'
LIMIT 10000;
```

For each batch: `r2.DeleteObjects` (up to 1000 keys/call), then `UPDATE jobs SET status='deleted' WHERE id = ANY($1)`. R2 404 after; Postgres row kept as tombstone for dedup history.

**Re-discovery.** If a crawler re-finds a job within grace, the canonical pipeline matches on `canonical_id`, flips `status='active'`, resets `expires_at`, emits `job.ready`. Same slug; no broken links.

**Force-remove (DMCA / abuse):** rare enough to do synchronously with a single-URL cache purge.

## Observability

Three signals. One SLO per user-visible path. Alerts only on SLO burn.

**Traces.** One root span per operation; children on each boundary:

| Root | Children |
|---|---|
| `publish.handle` | `snapshot.build`, `r2.put`, `pg.mark_published`, `cdn.purge` |
| `search.api` | `pg.fts_query`, `pg.facets_read` |
| `retention.stage1` | `pg.expire_batch` |
| `retention.stage2` | `pg.select_candidates`, `r2.delete_batch`, `pg.mark_deleted` |

Attributes: `job.slug`, `job.id`, `r2_version`, `job.category`, `bytes.in/out`, `status.code`. Use `pkg/telemetry`.

**Metrics (Prometheus).**

```
publish_total{result, step, category}                counter
publish_r2_put_ms{category}                          histogram
publish_end_to_end_ms                                histogram
publish_r2_bytes_total                               counter
publish_backlog                                      gauge

search_requests_total{endpoint, status}              counter
search_latency_ms{endpoint}                          histogram
search_pg_query_ms{kind}                             histogram

pg_mv_refresh_duration_seconds{matview}

r2_requests_total{op, status}                        (from CF analytics)
r2_bytes_transferred_total{op}
cdn_cache_hit_ratio{zone}

retention_expired_total                              counter
retention_deleted_total                              counter
retention_r2_errors_total                            counter
retention_backlog_days                               gauge
```

**Logs.** Structured JSON via `util.Log(ctx)`. Always include `trace_id`, `slug`, `canonical_id`, `r2_version`, `step`.

**SLOs.**

| Path | Indicator | Objective |
|---|---|---|
| Publish | `publish_end_to_end_ms` | p95 < 3s, p99 < 10s |
| Search API | `search_latency_ms` | p95 < 80ms, p99 < 200ms |
| Job detail | R2 GET latency (CF RUM) | p95 < 200ms hit / < 600ms miss |
| Freshness | time since `published_at` vs served | ≤ `s-maxage` + 30s |
| Retention lag | `retention_backlog_days` | < 1 day |

**Alerts.**

```
PublishFailures            publish failure ratio > 1% for 5 min          PAGE
PublishBacklog             publish_backlog > 5k for 10 min               TICKET
SearchSlow                 search p99 > 500ms for 5 min                  TICKET
SearchErrors               5xx ratio > 0.5% for 5 min                    PAGE
MVStale                    mv_job_facets refresh_age > 15 min            TICKET
CDNCacheMissSpike          hit ratio < 70% for 15 min                    TICKET
RetentionLag               retention_backlog_days > 2                    PAGE
R2ObjectGrowthDivergence   r2_objects vs active_jobs diverge > 10% 24h   TICKET
```

**Dashboard: one page, four rows** — Publish, Search, Edge, Retention.

## Build plan

Greenfield build. No dual-write, no backfill, no gradual cutover. Delete the old pieces, build the new ones, deploy. Existing Postgres rows will be re-populated by the crawler after the new pipeline is in place — easier than preserving intermediate state.

### What to remove up front

- `ui/content/jobs/*.md` (all four existing test files) — keep only a new `jobs.md` route shell.
- `ui/scripts/sync-r2.sh` and its CF Pages build hook.
- `[taxonomies] category` block in `hugo.toml`.
- `ui/data/stats.json` / `ui/data/categories.json` build-time fetches.
- `pkg/publish/markdown.go` — the `.md` rendering path is gone for good.
- Any R2 objects under `jobs/*.md` (lifecycle-delete or one-shot purge).
- Any OpenSearch client/index code in `search-api` if present.

### What to build

**Infra:**
- `job-repo.stawi.org` as its own CF zone bound to the R2 bucket (separate from `jobs.stawi.org` so cache operations don't cross-affect).
- Public R2 bucket `stawi-jobs-content` with CORS for `jobs.stawi.org` + local dev.

**Postgres:**
- Add columns: `status`, `expires_at`, `r2_version`, `search_tsv` (GENERATED STORED), `published_at` if missing.
- Partial indexes on `status='active'` for FTS, recency, category, skills.
- `mv_job_facets` materialized view with unique index on `(dim, key)`.

**Go:**
- `pkg/publish/snapshot.go` — `BuildSnapshot(*CanonicalJob) JobSnapshot`, pure function, golden-file tests.
- Evolve `PublishHandler` to: PUT `jobs/<slug>.json` → UPDATE PG (`published_at`, `r2_version`) → best-effort CF purge.
- `search-api` endpoints: `/api/search`, `/api/categories`, `/api/categories/<slug>/jobs`, `/api/jobs/latest`, `/api/stats/summary` — all Postgres-backed.
- `scheduler` cron handlers: Stage 1 expire (every 15 min), MV refresh (every 5 min), Stage 2 nightly retention.
- Admin endpoint `POST /admin/republish` for future bulk regenerations.

**Hugo shell:**
- `ui/static/_redirects` with catch-alls for `/jobs/*`, `/categories/*`, `/companies/*`.
- `ui/assets/js/`: `main.js` dispatcher, `api.js`, route modules (`job-detail.js`, `search.js`, `category.js`, `dashboard.js`), components (`user-state.js`, `skeleton.js`, `toast.js`), `auth.js`.
- Layouts converted to hydration skeletons: server-side HTML is the chrome, client fills in data.

### Validation (post-deploy)

- Crawl a handful of sources, confirm snapshots land in R2 and search surfaces them.
- Deep-link into `/jobs/<slug>/` from a search result → renders from R2.
- Expire a row manually (`UPDATE jobs SET expires_at = now() - interval '1 day' WHERE id = …`) → next Stage 1 run flips `status`; row leaves search; R2 still serves with expired banner.
- Force-delete a row → R2 404 + "not found" UX.
- Full-text, category, and remote filters produce sane results.
- SLO dashboard populated within the first hour.

### What this plan does not require

- No dual-write. No backfill. No feature flags.
- No downtime (the old site is test-only; swap is a DNS + deploy).
- No coordinated cross-service deploy beyond what CI already runs.
