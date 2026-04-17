# Scaling Hugo to Millions of Jobs — Implementation

> Spec: `docs/superpowers/specs/2026-04-17-hugo-millions-strategy-design.md`
>
> Greenfield execution per user direction: delete old paths outright, build new paths in place. No dual-write, no migration ceremony. Commit per logical unit.

## Execution order

1. Postgres schema: add `status`, `expires_at`, `r2_version`, `published_at`, `category`; convert `search_vector` to GENERATED; partial indexes; `mv_job_facets` MV.
2. Domain model: replace `IsActive` with `Status`; update all repository callers.
3. `JobSnapshot` type + `BuildSnapshot` pure function, unit-tested.
4. Markdown → sanitized HTML renderer (bluemonday + goldmark).
5. `CachePurger` Cloudflare client.
6. `R2Publisher.UploadPublicSnapshot` with `Cache-Control` + `PublicURL` helper + `ContentOrigin`.
7. Rewrite `PublishHandler`: R2 JSON upload, mark published, bump `r2_version`, best-effort purge. Delete `pkg/publish/markdown.go`.
8. `SearchCanonical` with cursor + filters + facets; `FacetRepository`; `RetentionRepository`.
9. API endpoints: `/api/search`, `/api/categories`, `/api/categories/{slug}/jobs`, `/api/jobs/latest`, `/api/stats/summary`, `/admin/republish`.
10. Scheduler crons: expire (15m), MV refresh (5m), retention Stage 2 (daily).
11. Hugo shell: delete per-job content, remove `sync-r2.sh`, drop taxonomies, add `_redirects`, add client JS (safe DOM construction), update layouts to hydration skeletons.
12. Infra (off-repo): CF Pages build command, R2 CORS, `jobs-repo.stawi.org` domain — document in README.

## Safety notes for the JS layer

- `description_html` is the only innerHTML sink, and it is pre-sanitized server-side via `bluemonday.UGCPolicy()`.
- All other interpolated fields go through `createElement` + `textContent` — never string concatenation into `innerHTML`.
- Auth tokens are never logged; API origin is same-host so cookies are omitted on R2 fetches.
