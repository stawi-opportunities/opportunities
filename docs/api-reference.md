# API reference

PostgreSQL is authoritative for opportunity search, details, crawl diagnostics,
candidate state, and application state.

Handler implementations under `apps/api/cmd` and `apps/matching` are the source
of truth for request fields and status codes. This page lists the primary
surfaces only.

## Public discovery (`apps/api`)

Gateway prefix (Cloud Run): **`/opportunities`** on `api.stawi.org` (replaces `/jobs`).
After strip_prefix, handlers are relative to the service root:

| Surface | Purpose |
|---------|---------|
| `GET /api/search` | Opportunity search (BM25 via `lakebase_text` on Neon) |
| `GET /api/opportunities/{slug}` | Opportunity detail by slug (**canonical**) |
| `GET /api/opportunities/{slug}/related` | Similar / related listings (same kind + title tokens; excludes self) |
| `GET /api/opportunities/top` | Top listings |
| `GET /api/opportunities/latest` | Latest listings |
| `GET /api/jobs/{slug}` | **Legacy alias** of detail (compat during cutover) |
| `GET /healthz` | Liveness |

Every opportunity returned by discovery includes a non-empty `apply_url` when
present in the serving row.

`SEARCH_BACKEND=lakebase_text` (default). Ranking uses `lakebase_bm25` when
the extension is present; otherwise `ts_rank` on the same `search_tsv` column
(tests / local Timescale). `pg_search` is retired.

## Candidate surface (`apps/matching`)

Requires JWT (OIDC). Gateway may strip a `/matching` prefix.

| Surface | Purpose |
|---------|---------|
| `GET /me/subscription` | Plan / paid status |
| `GET /me/opportunities` | Unified feed (matches + saved + applications) |
| `POST /me/chat` | Shared placement chat (prefs + qualifications intake). Body may include `context` (`placement` \| `opportunity`) and `opportunity{…}` for listing side-chat. Always delegates to platform **chat-agent** (`CHAT_AGENT_*` required; no local fallback — 503/502 on misconfig or agent errors). |
| `GET/PUT /me/onboarding` | Onboarding draft + message transcript |
| `PUT /me/cv` | CV → files service + sync placement summary |
| `GET /me/cv` | File-id ref + qualifications from placement summary |
| `POST /billing/checkout` | Start payment |
| `GET /billing/plans` | Plan catalog (public) |

## Crawl admin

| Service | Surface | Purpose |
|---------|---------|---------|
| crawler | `POST /admin/sources/{id}/crawl` | Dispatch one crawl (Trustage) |
| crawler | `GET /admin/crawl/status` | Queue depth, oldest age, pause |
| api | `/admin/*` sources, frontier, trace | Operator control plane |

Admin routes on API require an authenticated principal with the `admin` role
when JWT middleware is wired. Matching Trustage jobs use `X-Admin-Token`
(`ADMIN_SHARED_SECRET`).

## Extract contract (product-facing)

Clients should assume opportunities were produced by **structured extract**
(API, schema.org JobPosting, recipe, or spec connector) and accepted by
`pkg/crawlaccept`. There is no crawl-time AI stub path that invents listings.
