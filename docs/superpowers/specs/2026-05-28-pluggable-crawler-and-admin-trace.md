# Pluggable Crawler Customizations + Admin Trace Visibility

**Date:** 2026-05-28
**Status:** Approved (decisions: R2 + periodic refresh + NATS broadcast on update; separate `/admin` sub-app)

## Problem

Today the crawler is hand-tuned per source via Go code and YAML files baked into the Docker image. Adding a new job board, tweaking an extraction prompt, or fixing a connector requires a redeploy. Operators can't see what happened to a job from seed to publish without scraping logs across services.

Two gaps:

1. **Customization requires a deploy.** 13 connectors are Go structs registered in `apps/crawler/service/setup.go`. Kind specs + extraction prompts live in `definitions/opportunity-kinds/*.yaml` loaded once at boot. Source seeds live in `pkg/seeds/`.
2. **Lineage is invisible.** The data is there — `crawl_jobs`, `raw_payloads`, `pipeline_variants`, `opportunities`, plus Iceberg event log — but no endpoint walks the chain. "What did source X produce yesterday? What was rejected and why? Which variants joined which canonical?" all require manual SQL.

## Solution

Two orthogonal threads, each independently shippable:

**A. Definitions Service (`pkg/definitions`)** — single loader fronting kind specs, extraction prompts, source seeds, connector specs from R2. Periodic refresh (5 min) + NATS broadcast for instant-on-edit. Admin endpoints for list/get/put/rollback. Two new spec-driven connectors (`htmllisting`, `jsonfeed`) so most new sources need no Go code.

**B. Trace API + Admin sub-app** — five admin endpoints (source trace, variant trace, opportunity trace, seed digest, raw_payload body) that join `crawl_jobs` × `raw_payloads` × `pipeline_variants` × `opportunities` × Iceberg historic to produce a unified timeline. Separate `/admin` sub-app (Hugo route + React island) displays them.

## Architecture

```
                   ┌─────────────────────────────────────────────┐
                   │                R2 bucket                    │
                   │  definitions/                               │
                   │    kinds/{kind}.yaml                        │
                   │    prompts/{kind}.txt          (extracted)  │
                   │    connectors/{name}.yaml      (NEW)        │
                   │    seeds/{source}.yaml         (NEW)        │
                   │  Versioning via R2 object versioning.       │
                   └─────────────────────────────────────────────┘
                              ▲                  │  fetch on
                              │ PUT              │  5-min refresh
                       admin ops               crawler / api / worker
                              │
                              │ emits
                              ▼
                ┌────────────────────────────┐
                │   NATS: definitions.       │
                │   changed.v1               │
                │   {type, name, version}    │
                └────────────────────────────┘
                              │ broadcast
                              ▼
                consumers reload that one item

  ┌─────────────────────── Trace data flow ───────────────────────┐
  │                                                                │
  │  sources ─→ crawl_jobs ─→ raw_payloads ─→ pipeline_variants  │
  │     (id)     (source_id)   (crawl_job_id)   (raw_payload_id,  │
  │                                              crawl_job_id,     │
  │                                              canonical_id)     │
  │                                                  │             │
  │                                                  ├─→ opportunities (canonical) → R2 published JSON
  │                                                  │                                                  
  │                                                  └─→ Iceberg variants_rejected (durable)            
  │                                                                                                     
  │  Trace endpoint walks all four tables + Iceberg for historic.                                       
  └─────────────────────────────────────────────────────────────────┘
```

## Phasing

Six phases, each operator-visible on its own:

| Phase | Scope | Estimate |
|---|---|---|
| **L1** | B — source/variant/opportunity trace endpoints + basic admin page | 4-5 days |
| **L2** | B — rejection drill-down (raw_payload_id on event + R2 body fetch endpoint) | 1-2 days |
| **L3** | A — `pkg/definitions` R2 loader + refresh + NATS broadcast + admin endpoints + per-source extraction_prompt_extension | 5-6 days |
| **L4** | A — six spec-driven connectors (`htmllisting`, `jsonfeed`, `rssfeed`, `sitemap`, `schemaorgjsonld`, `xmlfeed`) | 5-7 days |
| **L5** | B — Iceberg query path for historic (>7 d) trace; seed digest | 2-3 days |
| **L6** | UI polish — admin sub-app pages for all the above | 2-3 days |

Total: ~3-4 weeks. Each phase ships independently.

---

## Component Designs

### A. Definitions Service

#### A.1 `pkg/definitions` — the loader

A new package with this surface:

```go
// Type is the discriminator for what kind of definition we're loading.
type Type string
const (
    TypeKind       Type = "kind"       // opportunity-kind spec YAML
    TypePrompt     Type = "prompt"     // extraction-prompt text (extracted from kind YAML's extraction_prompt field today; promoted to a standalone artifact)
    TypeConnector  Type = "connector"  // declarative connector spec
    TypeSeed       Type = "seed"       // a single source seed YAML
)

// Loader is the public face. Production constructs one with NewR2Loader;
// tests use NewMemoryLoader.
type Loader interface {
    // Get returns the active body + version. Errors only on configuration
    // failure (loader not initialized); a missing definition returns
    // (nil, "", ErrNotFound) which is a valid result for "this name was
    // deleted".
    Get(ctx context.Context, t Type, name string) (body []byte, version string, err error)

    // List enumerates active definitions of a type. Used by /admin/definitions.
    List(ctx context.Context, t Type) ([]Entry, error)

    // Subscribe registers a callback for definitions.changed.v1 events.
    // The callback runs on the loader's NATS goroutine; it MUST be
    // non-blocking. Used by the in-process consumers (kind registry,
    // connector registry) to invalidate their own caches.
    Subscribe(t Type, fn func(name string, version string))
}

type Entry struct {
    Name     string
    Version  string  // R2 object version id
    UpdatedAt time.Time
    Size     int64
}

var ErrNotFound = errors.New("definitions: not found")
```

The R2 loader implementation:
- Lists objects under `definitions/{type}/` on a 5-min ticker.
- Maintains an in-process map keyed by (type, name) → (body, version, fetched_at).
- On a NATS `definitions.changed.v1` event with `(type, name)`, immediately re-fetches that one object and fires Subscribers.
- Treats R2 transient errors as cache-stale (serve last good); logs WARN; retries on next tick.

#### A.2 Migration of existing definitions

Today's `definitions/opportunity-kinds/*.yaml` live in the repo + Docker image. Migration is:

1. The repo files become the **bootstrap** — an init container copies them to R2 on first deploy at `definitions/kinds/{kind}.yaml`. R2 object versioning preserves history.
2. The crawler/api/worker drop the static `LoadFromDir` boot read in favour of the loader's first-fetch.
3. The repo files stay as the "source of truth on disk" for reviewable git history; a `make sync-definitions` target uploads them. Operators with admin access can also edit via the admin UI; the next git sync overwrites unless operator edits land back in the repo.

(Decision: git is the **canonical** source. Admin-UI edits are emergency lever; the operator is responsible for back-porting to git or accepting that a redeploy reverts them. This keeps long-term auditability without forcing every edit through PR.)

#### A.3 NATS broadcast

New event topic `opportunities.definitions.changed.v1`:

```go
type DefinitionsChangedV1 struct {
    Type      string    `json:"type"`     // kind|prompt|connector|seed
    Name      string    `json:"name"`
    Version   string    `json:"version"`  // R2 object version id
    Action    string    `json:"action"`   // upsert|delete
    ChangedAt time.Time `json:"changed_at"`
    ChangedBy string    `json:"changed_by,omitempty"` // admin user subject
}
```

Emitted by the admin endpoints (`PUT`, `DELETE`, `rollback`). Consumers (crawler, api, worker, materializer) subscribe via the `Loader.Subscribe` API.

#### A.4 Admin endpoints

Mounted on the API service at `/admin/definitions/*`:

| Method | Path | Behaviour |
|---|---|---|
| `GET` | `/admin/definitions?type=kind` | List names + active version |
| `GET` | `/admin/definitions/{type}/{name}` | Return active body + version |
| `GET` | `/admin/definitions/{type}/{name}/versions` | List historic versions (R2 object versions) |
| `PUT` | `/admin/definitions/{type}/{name}` | Upload new body (multipart); validate against schema; emit NATS event |
| `POST` | `/admin/definitions/{type}/{name}/rollback?to=<version>` | Make a prior R2 version the active one (writes a new version with that content); emit NATS event |
| `DELETE` | `/admin/definitions/{type}/{name}` | Soft-delete (delete-marker on R2); emit NATS event |
| `POST` | `/admin/definitions/reload` | Force-refresh on all replicas (broadcasts a wildcard `definitions.changed.v1`) |

All `requireAdmin`-guarded.

Schema validation per type happens server-side:
- `kind`: validates via `pkg/opportunity.Spec` unmarshal + `Spec.Validate()`.
- `prompt`: non-empty, ≤ 16 KB.
- `connector`: validates against `pkg/connectors/spec.ConnectorSpec`.
- `seed`: validates against `pkg/seeds.SeedSpec`.

Bad uploads return 400 with the validation error inline.

#### A.5 Spec-driven connectors

The goal: cover **every public-feed format job boards routinely expose**, so adding a new source = drop a YAML in R2, no Go change. Six spec types, all sharing a common base in `pkg/connectors/spec`:

| Type | Format | Typical job-board examples |
|---|---|---|
| `htmllisting` | HTML page + CSS selectors | Boards without an API; most listing pages |
| `jsonfeed` | JSON URL + JSONPath | REST APIs, JSON Feed (jsonfeed.org), Greenhouse-style boards |
| `rssfeed` | RSS 2.0 / Atom | WordPress careers pages, Workable, AngelList, many ATS providers |
| `sitemap` | `sitemap.xml` (W3C) | Most large boards expose one; the existing `sitemapcrawler` becomes a special case of this |
| `schemaorgjsonld` | HTML → embedded `<script type="application/ld+json">` JobPosting | Google-for-Jobs-compliant pages — increasingly the norm |
| `xmlfeed` | Arbitrary XML + XPath | Indeed XML, Aggregator-style feeds |

All six share:

- A `Connector` impl in `pkg/connectors/spec` whose `Crawl(ctx, src)` dispatches based on `spec.type`.
- A common `Pagination` block (kind: page / offset / cursor / next-link / none) reused across all six.
- The same `fields:` map shape — the underlying extractor (CSS / JSONPath / Atom field / XPath / JSON-LD key) is per-type but the YAML keys are consistent.
- Politeness throttle (`delay_ms`) and per-page timeout (`timeout_ms`) shared.
- Registration under a single placeholder type `spec`; the loader picks the concrete impl from `spec.type`.

Spec examples (one per format):

```yaml
# RSS — Workable careers feed
type: rssfeed
list_url: https://apply.workable.com/api/v1/widget/accounts/example/feed
fields:
  title: title
  apply_url: link
  description: description
  posted_at: pubDate

# Sitemap — declarative version of pkg/connectors/sitemapcrawler
type: sitemap
list_url: https://example.com/sitemap.xml
include_patterns: ["/jobs/", "/careers/"]
exclude_patterns: ["/jobs/archive/"]
follow_index: true                 # follow nested <sitemapindex> entries
detail_fetch: true                 # GET each candidate URL and run schemaorgjsonld on the body
detail_fallback_type: schemaorgjsonld

# Schema.org JobPosting JSON-LD
type: schemaorgjsonld
list_url: https://example.com/jobs/{slug}
# When list_url has a {slug}, sitemap or htmllisting feeds the slug
# stream; otherwise list_url is hit directly and JSON-LD blocks
# returned in-document are emitted as items.

# Generic XML — Indeed XML feed
type: xmlfeed
list_url: https://feed.example.com/jobs.xml
item_xpath: //source/job
fields:
  title: title/text()
  apply_url: url/text()
  description: description/text()
  posted_at: date/text()
```

Implementation notes:

- `htmllisting` + `schemaorgjsonld` share `goquery` for HTML parsing (already a transitive dep).
- `rssfeed` + `xmlfeed` share `github.com/mmcdole/gofeed` for Atom/RSS 2.0 + raw XML; gofeed is well-maintained and covers ~all real-world RSS quirks.
- `sitemap` reuses `pkg/connectors/sitemapcrawler` parsing internals (refactor to expose them publicly) — that connector becomes a Go thunk that dispatches a `sitemap`-typed spec, ensuring no regression on the existing 13 sources.
- `jsonfeed` uses `github.com/PaesslerAG/jsonpath` (small dep, ~300 LoC).

Registration: the connector registry rebuilds on `opportunities.definitions.changed.v1` for `type=connector`. Loading errors (malformed spec, unknown `spec.type`) are logged WARN and don't crash the crawler — the offending connector is dropped from the registry; its sources will fall back to whatever existing connector handles `source.type` (or fail Verify with an actionable error).

**Migration of existing connectors:** the 13 Go connectors stay in place. We don't rewrite them as specs — they handle ATS-specific OAuth flows or per-vendor quirks that don't fit the declarative model. The new spec connectors are for the **long tail** of HTML/RSS/sitemap pages that don't justify hand-written Go.

#### A.6 Per-source prompt extensions

The kind-level extraction prompt (in `definitions/opportunity-kinds/{kind}.yaml`) is one template for all sources of that kind. But sources have quirks: Brightermonday encodes city names in a sidebar widget, Workday wraps salary in a `data-` attribute, Naukri lists multiple openings per page that need a "pick the first" instruction. Today the only escape hatch is editing the shared kind prompt — which affects every source of that kind.

**Solution:** add a per-source string appended to the kind prompt at extract time.

**Schema:**

```sql
ALTER TABLE sources
    ADD COLUMN IF NOT EXISTS extraction_prompt_extension TEXT NOT NULL DEFAULT '';
```

(Migration added to `apps/crawler/migrations/0001/` in Plan B.)

**Storage path:** edit-via-admin lands in the `sources` row directly; the source-seed YAML format also grows an optional `extraction_prompt_extension` field so seeds-from-git can pre-populate it. On seed upsert, repo content overwrites if changed (operator can edit in admin UI; next git sync overwrites unless back-ported — same contract as definitions).

**Wiring:** `pkg/extraction.Extractor.Extract(ctx, body, kinds, sourceExtension)` learns an optional last parameter. When non-empty, it's appended after the kind prompt:

```
{universal prefix}

{kind extraction_prompt}

{source-specific extension, prefixed with: "Additional instructions for THIS source:"}

{body}
```

The crawler's `enrichOne` reads `src.ExtractionPromptExtension` from the looked-up source and passes it through. The crawler-side queue / Plan-2 enricher worker does the same.

**Admin endpoints:** the existing `PUT /admin/sources/{id}` accepts `extraction_prompt_extension` in its body (currently the sources_admin.go handler takes a struct with `RequiredAttributesByKind` etc.; this is one more field). Edit triggers no rebuild — the next crawl reads the column. No NATS broadcast needed (the field is per-row and read from the database on each crawl.request).

**UI surface:** the admin UI's source page gets a textarea labelled "Extraction prompt extension" with a preview that shows the rendered combined prompt (kind base + extension). Length cap: 4 KB (a sentence or two of guidance, not a rewrite).

#### A.7 Wiring

`apps/crawler/service/setup.go` (`BuildRegistry`) gets a final loop:

```go
specs, err := defsLoader.List(ctx, definitions.TypeConnector)
if err != nil { /* warn + continue */ }
for _, e := range specs {
    body, _, _ := defsLoader.Get(ctx, definitions.TypeConnector, e.Name)
    c, err := spec.NewFromYAML(e.Name, body, httpClient)
    if err != nil { /* warn + skip */ continue }
    reg.Register(c)
}
```

Plus a subscription that re-runs that loop on `definitions.changed.v1` type=connector.

The existing kind registry (`pkg/opportunity.LoadFromDir`) gets replaced with a `LoadFromDefinitions(loader)` constructor.

---

### B. Trace API + Admin sub-app

#### B.1 Trace endpoints

All under `/admin/trace/*` on the API service, `requireAdmin`-guarded:

| Method | Path | Returns |
|---|---|---|
| `GET` | `/admin/trace/sources/{id}?since=24h&limit=50` | Source metadata + summary + recent crawl_jobs |
| `GET` | `/admin/trace/variants/{variant_id}` | Single variant timeline (ingest → publish) |
| `GET` | `/admin/trace/opportunities/{slug}` | Canonical lineage — all variants that joined |
| `GET` | `/admin/trace/seeds/{id}/digest?date=YYYY-MM-DD` | Daily rollup |
| `GET` | `/admin/raw_payloads/{id}/body` | gzipped HTML from R2 (auth-required) |

Detailed shapes:

**`GET /admin/trace/sources/{id}`** response:

```json
{
  "source": {
    "id": "src-001",
    "type": "greenhouse",
    "base_url": "https://boards.greenhouse.io/acme",
    "country": "US",
    "status": "active",
    "health_score": 0.95,
    "next_crawl_at": "2026-05-28T13:00:00Z",
    "last_seen_at": "2026-05-28T12:55:00Z"
  },
  "summary": {
    "window": "24h",
    "crawl_jobs":         48,
    "crawl_jobs_failed":  2,
    "raw_payloads":       96,
    "variants_emitted":   1240,
    "variants_rejected":  87,
    "variants_published": 132,
    "rejection_reasons": {
      "missing_apply_url": 45,
      "missing_title":     32,
      "unknown_kind":      10
    }
  },
  "recent_crawls": [
    {
      "crawl_job_id":   "...",
      "scheduled_at":   "2026-05-28T12:55:00Z",
      "started_at":     "2026-05-28T12:55:01Z",
      "finished_at":    "2026-05-28T12:55:04Z",
      "duration_ms":    2840,
      "status":         "succeeded",
      "jobs_found":     26,
      "jobs_stored":    24,
      "raw_payloads":   1,
      "error_code":     null
    }
  ]
}
```

Implementation: 2 Postgres queries — one against `crawl_jobs` for recent rows, one aggregate against the join chain for summary. Iceberg fetch for rejection-reason histogram beyond Postgres retention.

**`GET /admin/trace/variants/{variant_id}`** response:

```json
{
  "variant_id":   "var-abc",
  "external_id":  "greenhouse:acme:eng-2026",
  "hard_key":     "...",
  "source":       { "id": "src-001", "type": "greenhouse" },
  "crawl_job":    { "id": "cj-...", "scheduled_at": "..." },
  "raw_payload":  {
    "id":           "rp-...",
    "source_url":   "https://example.com/jobs",
    "storage_uri":  "raw/abc123.html.gz",
    "content_hash": "abc123",
    "size_bytes":   18432,
    "fetched_at":   "...",
    "http_status":  200,
    "body_url":     "/admin/raw_payloads/rp-.../body"
  },
  "timeline": [
    { "stage": "ingested",   "at": "2026-05-28T12:55:01.230Z", "duration_ms": null },
    { "stage": "normalized", "at": "2026-05-28T12:55:01.354Z", "duration_ms": 124 },
    { "stage": "validated",  "at": "2026-05-28T12:55:02.244Z", "duration_ms": 890 },
    { "stage": "clustered",  "at": "2026-05-28T12:55:02.285Z", "duration_ms": 41,  "canonical_id": "opp-xyz" },
    { "stage": "canonical",  "at": "2026-05-28T12:55:02.303Z", "duration_ms": 18 },
    { "stage": "published",  "at": "2026-05-28T12:55:02.533Z", "duration_ms": 230, "r2_key": "jobs/acme-eng.json" }
  ],
  "current_stage":     "published",
  "opportunity_slug":  "acme-eng-2026",
  "attempts":          { "normalize": 1, "validate": 1, "publish": 1 },
  "last_error":        null
}
```

If the variant was rejected, the timeline ends at the rejection stage with the reasons inline:

```json
"timeline": [
  { "stage": "ingested", ... },
  { "stage": "rejected", "at": "...", "reasons": ["missing_apply_url"], "by": "opportunity.Verify" }
],
"current_stage": "rejected"
```

Implementation: `pipeline_variants` row joined with `crawl_jobs` + `raw_payloads` + `opportunities`. Timeline derived from `stage_at` history (the `attempts` JSONB carries retry counts; for actual transition timestamps we need a transition log — see B.2).

**`GET /admin/trace/opportunities/{slug}`** response:

```json
{
  "opportunity": {
    "canonical_id":  "opp-xyz",
    "slug":          "acme-eng-2026",
    "kind":          "job",
    "title":         "Senior Engineer",
    "first_seen_at": "...",
    "last_seen_at":  "...",
    "published_url": "https://opportunities-data.stawi.org/jobs/acme-eng-2026.json"
  },
  "variant_count": 4,
  "variants": [
    {
      "variant_id":   "var-1",
      "source":       { "id": "src-001", "type": "greenhouse" },
      "ingested_at":  "...",
      "joined_at":    "...",
      "is_primary":   true
    },
    { "variant_id": "var-2", "source": { "id": "src-007", "type": "indeed" }, ... }
  ]
}
```

**`GET /admin/trace/seeds/{id}/digest?date=2026-05-28`** response:

```json
{
  "source_id":   "src-001",
  "date":        "2026-05-28",
  "crawl_jobs":  48,
  "variants_emitted":   1240,
  "variants_rejected":  87,
  "variants_published": 132,
  "rejection_reasons":  { "missing_apply_url": 45, ... },
  "newly_canonical":    30,
  "deduped_into_existing": 102,
  "newly_published":    7,
  "data_source":        "postgres"  
}
```

`data_source: "iceberg"` when the date is beyond 7-day Postgres retention.

#### B.2 Stage-transition audit log

The existing `pipeline_variants` row only carries `current_stage` + `stage_at` (current state). For a useful timeline we need the transition history.

Two options:

- **Option 1** — A new `pipeline_variant_transitions(variant_id, ingested_at, from_stage, to_stage, transitioned_at, duration_ms, attempt, error)` table (hypertable, 7-d retention).
- **Option 2** — Read transitions from the Iceberg event log (`opportunities.variants_normalized`, `…_validated`, etc. tables already exist via the writer subscription).

Recommendation: **Option 2** for now. The events ARE the audit log. Adding a new Postgres table duplicates state and bloats inserts on the hot path. The trace endpoint reads recent transitions from Postgres (`pipeline_variants.stage_at`) for the latest stage and back-fills historic transitions from Iceberg when needed.

Trade-off: Iceberg queries are slower than Postgres (~hundreds of ms). For "show me variant X right now" that's fine; for high-frequency dashboards we cache the result.

#### B.3 Rejection drill-down

Today `VariantRejectedV1` carries `variant_id`, `source_id`, `kind`, `title`, `reasons[]`. It does NOT carry `raw_payload_id` — that link is needed for "click on rejection → see the HTML it failed on".

**Schema change**: add `raw_payload_id string` and `crawl_job_id string` to `VariantRejectedV1`. The crawler's `publishRejected` populates them from the current `crawlJob.ID` + `pageRawPayloadID` (already in scope at the call site). The writer's `BuildVariantRejectedRecord` learns to emit the new columns to the Iceberg `variants_rejected` table.

Backwards-compat: events emitted before the change have NULL for these fields in Iceberg. Admin UI shows "(unknown — pre-trace deploy)" for those.

#### B.4 `/admin/raw_payloads/{id}/body`

```go
func (h *adminHandlers) RawPayloadBody(w http.ResponseWriter, r *http.Request) {
    id := r.PathValue("id")
    payload, err := h.crawlRepo.GetRawPayload(ctx, id)
    if err != nil { http.Error(w, ..., 404); return }
    body, err := h.archive.GetRaw(ctx, payload.ContentHash)
    if err != nil { http.Error(w, ..., 502); return }
    w.Header().Set("Content-Type", "text/html; charset=utf-8")
    w.Header().Set("Content-Encoding", "gzip")
    w.Header().Set("X-Source-URL", payload.SourceURL)
    w.Write(body) // body is already gzipped in R2
}
```

Operator opens in a browser. The R2 blob is content-addressed and immutable, so safe to cache via `Cache-Control: private, max-age=3600`.

#### B.5 Admin sub-app (`/admin/*`)

Separate sub-app, not bolted onto the candidate-facing dashboard. Lives in `ui/admin/`:

```
ui/admin/
├── index.html         (Hugo page; mounts admin React app)
├── src/
│   ├── main.tsx
│   ├── pages/
│   │   ├── SourceTrace.tsx
│   │   ├── VariantTrace.tsx
│   │   ├── OpportunityTrace.tsx
│   │   ├── SeedDigest.tsx
│   │   ├── DefinitionsList.tsx
│   │   ├── DefinitionEditor.tsx
│   │   └── RawPayloadViewer.tsx
│   ├── components/
│   │   ├── TraceTimeline.tsx
│   │   ├── RejectionChart.tsx
│   │   └── DefinitionHistory.tsx
│   └── api/admin-client.ts
├── tsconfig.json
└── vite.config.ts
```

Routes:

| Path | Page |
|---|---|
| `/admin/` | landing — list of sources + their status |
| `/admin/sources/{id}` | source trace view (B.1 endpoint #1) |
| `/admin/variants/{id}` | variant trace view (timeline) |
| `/admin/opportunities/{slug}` | canonical lineage |
| `/admin/seeds/{id}/digest` | daily/weekly rollup chart |
| `/admin/definitions` | list all definitions by type |
| `/admin/definitions/{type}/{name}` | view + edit one definition |
| `/admin/dlq` | (future — out of scope here) |

**Auth — role check, not a separate auth flow.** Same OAuth2 / Hydra flow as the candidate dashboard. Authorization is by JWT claim: a user with the `admin` role in their token gets access; everyone else gets 404.

Two enforcement points, both reading the same JWT:

1. **API side** (every `/admin/*` endpoint): existing `requireAdmin` middleware extracts the security claims, checks for the `admin` role, returns 403 otherwise. This is the security boundary — if it's broken, the data leaks regardless of UI.
2. **UI side** (`/admin/` mount): the JS auth runtime already in use (`@stawi/auth-runtime`, source at `/home/j/code/stawi.dev/widgets.js/shared/auth-runtime/`) exposes `getRoles(): Promise<string[]>` (`runtime.ts:60`). It reads the role list from the JWT — supporting both the direct `roles: []` claim shape and Keycloak's nested `realm_access.roles: []` (`shared/jwt.ts:12-20`). The admin React shell calls `auth.getRoles()` on mount and re-routes to `/` (or shows 404) if the array doesn't include `"admin"`. This is defense-in-depth — the security boundary is on the API; the UI check just hides the page from non-admins.

Two consequences:
- Hugo serves the admin shell unconditionally (no server-side gate). The cost is one extra request from a curious non-admin who finds the URL; the API returns 403 to all data calls so they see an empty/error page. Simpler than threading the role into the Hugo template.
- Granting admin to a user is a single Hydra console edit — add `admin` to the user's roles claim. No new IdP, no new role table, no new permission system.

Vite build emits to `static/admin-app/` (parallel to the existing `static/app/`). The Hugo build picks it up automatically.

---

## Data Model Changes

| Change | Migration | Notes |
|---|---|---|
| Add `raw_payload_id` + `crawl_job_id` to `VariantRejectedV1` payload | None (event schema only) | Writer's Iceberg schema gets two new optional STRING columns |
| Iceberg table `variants_rejected` adds 2 optional columns | Writer code (BuildVariantRejectedRecord) | Iceberg schema evolution is backwards-compatible — old rows have NULL |
| `definitions` cache table (optional, for fast `GET /admin/definitions`) | Migration adding `definitions_cache(type, name, version, body_size, etag, updated_at)` | Optional speedup; if missing the loader falls back to direct R2 ListObjects which is also fine at our scale (~100 objects total) |

No schema changes to `pipeline_variants`, `crawl_jobs`, `raw_payloads`, `opportunities`. Phase 1 + Plan 1/2/3 already laid the join columns.

## Error Handling

- **R2 down**: loader serves last good cached body, logs WARN, retries on next tick. Admin endpoints return 503 on direct R2 calls (uploads, downloads).
- **NATS broadcast delivery**: best-effort. If a replica misses the event, the 5-min refresh catches it.
- **Bad definition uploaded**: server-side schema validation rejects with 400; nothing reaches the cache.
- **Variant not found**: 404 with structured error.
- **Iceberg query timeout**: trace endpoint returns the Postgres-only view with `data_source: "postgres-only"` flag + a warning header.
- **Rollback to non-existent version**: 404.

## Testing

| Layer | Strategy |
|---|---|
| `pkg/definitions` | Unit tests with `MemoryLoader` for the cache + refresh + subscribe semantics; one integration test against minio for the R2 path |
| `htmllisting` / `jsonfeed` connectors | Golden-file tests: spec YAML + a fixture HTML/JSON + expected emitted items |
| Trace endpoints | Integration tests with ephemeral Postgres (existing `tests/integration` scaffold) seeding crawl_jobs/raw_payloads/pipeline_variants + asserting the JSON shape |
| Admin UI | Visual regression on the three main pages via Playwright; happy-path E2E that loads a source trace and clicks through |
| Iceberg historic path | Mocked via the `pkg/icebergclient` interface; real-iceberg covered by a separate integration test |

## Migration Path

1. **L1** ships: trace endpoints + a minimal admin page. Operators can drill into a source's recent crawls without any config change.
2. **L2** ships: rejected events carry `raw_payload_id`, admin UI links to `/admin/raw_payloads/{id}/body`. Operators can debug rejections by reading the actual HTML.
3. **L3** ships: definitions service. Existing YAML files are uploaded to R2 by the init container; nothing user-visible changes yet. Crawler/api/worker switch to reading from R2 (with refresh + NATS).
4. **L4** ships: spec-driven connectors. Operator can drop a YAML for a new source-type and crawl it on the next 5-min tick.
5. **L5** ships: Iceberg historic queries land. Trace endpoints honour `?since=` beyond 7 days.
6. **L6** ships: admin UI polish — charts, timeline visualisation, side-by-side rejection drill-down.

## Out of Scope (Not in this design)

- DLQ inspector + replay (separate spec).
- Multi-tenancy on definitions (every definition is global to the deployment).
- WASM / Go plugin runtime for fully-custom connectors. Operators write Go for connectors that don't fit `htmllisting` or `jsonfeed`.
- Rate-limit definitions per connector at runtime (the `delay_ms` in spec is a first cut; richer rate-limit policy is a follow-up).
- Definition diff visualization on the admin UI (operator can `git diff` against the canonical repo files for now).
- Source seeds-via-UI editing — seeds stay in git for L3; admin UI gets editing in a later phase.
- Embeddings / matching changes.

## Open Decisions

None — the three earlier-locked decisions (R2 + periodic + NATS; separate /admin) cover the design.
