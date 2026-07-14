# API reference

PostgreSQL is authoritative for opportunity search, details, crawl diagnostics,
candidate state, and application state.

Handler implementations under `apps/api/cmd` and `apps/matching` are the source
of truth for request fields and status codes. This page lists the primary
surfaces only.

## Public discovery (`apps/api`)

| Surface | Purpose |
|---------|---------|
| `GET /api/search` | Opportunity search |
| `GET /api/jobs/{slug}` | Job detail by slug |
| `GET /healthz` | Liveness |

Every opportunity returned by discovery includes a non-empty `apply_url` when
present in the serving row.

## Candidate surface (`apps/matching`)

Requires JWT (OIDC). Gateway may strip a `/matching` prefix.

| Surface | Purpose |
|---------|---------|
| `GET /me/subscription` | Plan / paid status |
| `GET /me/opportunities` | Unified feed (matches + saved + applications) |
| `POST /me/chat` | Shared placement chat (prefs + qualifications intake) |
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
