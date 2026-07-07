# API reference

PostgreSQL is authoritative for opportunity search, details, crawl diagnostics,
candidate state, and application state. The public API serves current rows from
`opportunities`; admin trace endpoints join the PostgreSQL ingestion tables.

Primary surfaces:

- `/api/v2/search` and `/api/v2/jobs/{slug}`: opportunity discovery and details.
- `/candidates/match`, `/api/me/matches`, and `/me/opportunities`: candidate
  matching and application-ready feeds.
- `/admin/crawl/status`: ingestion depth, oldest work age, and configured limits.
- `/admin/crawl_jobs`: source crawl history.
- `/admin/trace/*`: ingestion and source lineage diagnostics.

Admin routes require an authenticated principal with the `admin` role. Handler
implementations in `apps/api/cmd` are the source of truth for request fields and
status codes.

Every opportunity returned by discovery, detail, match, feed, or match-event
surfaces includes a non-empty `apply_url`. Clients do not need a second lookup
before starting an application.
