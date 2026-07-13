# Crawl framework — onboarding sources

Contract for adding coverage:

> **Onboarding a new source is a DATA change (recipe / seed / stock recipe), not a new Go package.**  
> Prefer free structured data (JSON API recipe, sitemap + JobPosting JSON-LD) before HTML recipes.

## Extract policy

**Structured only.** Incomplete records fail `pkg/crawlaccept` and are not stored.

| Prefer | Mechanism |
|--------|-----------|
| 1. Official JSON API | Engine type `api` + **recipe** (`acquisition: "api"`) or stock recipe |
| 2. Sitemap of job URLs | Engine type `sitemap` — detail pages should expose schema.org JobPosting |
| 3. JobPosting JSON-LD | Engine type `schema_org` |
| 4. Stable HTML listing + detail | Engine type `generic_html` + **recipe** |
| 5. Declarative feed/spec | R2 connector YAML under `definitions/connector/` |

LLM is for **recipe generation** (optional ops path), not for live job inventing.

## Engines only (code)

| Engine type | Go role |
|-------------|---------|
| `api` | Requires recipe — executed by `pkg/recipe.Executor` |
| `schema_org` | JSON-LD JobPosting extract |
| `sitemap` | Sitemap walk + structured detail |
| `generic_html` | Requires recipe for list+detail |
| `workday` / `smartrecruiters_api` | ATS engines |

There are **no** site-specific packages. Public APIs ship as **stock recipes** under `definitions/stock-recipes/` and attach by seed `recipe` field, host match, or admin create.

## Recipes (generic engine + per-source data)

```text
per source  →  RECIPE JSON on the source row  (or stock recipe name)
                    │
                    ▼
              pkg/recipe Executor
              api | sitemap | html-listing
```

Field extraction vocabulary:  
`json_ld`, `next_data`, `microdata`, `selector`, `xpath`, `meta`, `record`, `const`, `page_url`, `tenant`  
plus transforms (`trim`, `absolute_url`, `parse_date`, …).

Multi-tenant ATS boards: **one** source with a tenant list in the recipe — do not mint one source per company board.

## Method selection

```text
1. Official JSON API?     → type api + recipe (or stock recipe)
2. Sitemap of jobs?       → type sitemap
3. Detail JSON-LD?        → type schema_org
4. Stable HTML only?      → type generic_html + recipe
5. None of the above?     → do not crawl until a recipe exists
```

Never invent apply URLs from the board homepage. Never emit title-empty URL stubs.

## Seeds

`seeds/**/*.json` load at crawler boot into `sources`. `source_type` must be an engine (`api`, `schema_org`, `sitemap`, `generic_html`, `workday`, `smartrecruiters_api`). Optional `"recipe": "remoteok"` installs a stock recipe after upsert.

Historical site-named types (`jobberman`, `remoteok`, …) are **legacy data**. Crawler boot and the migrate job remap them to engines (and drop duplicates when an engine-typed row already exists for the same `base_url`).

## Admin

Create source → choose engine → optional stock recipe → verify/approve → test extract → crawl.  
Recipe tab: edit JSON, live dry-run, activate, history/rollback, queue AI generate.

## Trustage maintenance

| Workflow | Purpose |
|----------|---------|
| per-source crawl schedule | Dispatch crawl |
| source-crawl-overdue | Catch-up |
| crawl-runs-sweep | Resume/fail stuck runs |
| crawl-watchdog | Stall detection |
| sources-recipe-backfill | Queue recipe generation for HTML boards |

## Related

- [crawl-pipeline.md](./crawl-pipeline.md)
- [first-deploy-runbook.md](./first-deploy-runbook.md)
- [capacity-planning.md](./capacity-planning.md)
