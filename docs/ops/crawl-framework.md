# Crawl framework — onboarding sources

Contract for adding coverage:

> **Onboarding a new source is a DATA change (recipe / seed / spec), not a new Go package.**  
> Prefer free structured data (JSON API, sitemap + JobPosting JSON-LD) before HTML recipes.

## Extract policy

**Structured only.** Incomplete records fail `pkg/crawlaccept` and are not stored.

| Prefer | Mechanism |
|--------|-----------|
| 1. Official JSON API | Hand connector or `api` recipe |
| 2. Sitemap of job URLs | `sitemap` source type — detail pages must expose schema.org JobPosting |
| 3. JobPosting JSON-LD on listing/detail | `schema_org` or HTML board type via `pkg/connectors/structured` |
| 4. Stable HTML listing + detail | Extraction **recipe** (`pkg/recipe`) |
| 5. Declarative feed/spec | R2 connector YAML under `definitions/connector/` |

LLM is for **recipe generation** (optional offline/ops path), not for live job inventing.

## Services involved

| Service | Role |
|---------|------|
| Trustage | Per-source crawl cron + fleet watchdogs |
| `apps/crawler` | Execute crawl, accept, enqueue |
| `apps/frontier-worker` | Polite per-URL JSON-LD extract when frontier is enabled |
| `apps/worker` | Materialize queue → serving tables |

## Recipes (generic engine + per-source data)

```text
per source  →  RECIPE JSON on the source row
                    │
                    ▼
              pkg/recipe Executor
              api | sitemap | html-listing
```

Field extraction vocabulary (order, first non-empty wins):  
`json_ld`, `next_data`, `microdata`, `selector`, `xpath`, `meta`, `record`, `const`, `page_url`, `tenant`  
plus transforms (`trim`, `absolute_url`, `parse_date`, …).

Multi-tenant ATS boards (Greenhouse / Lever / Ashby style): **one** source with a tenant list in the recipe — do not mint one source per company board.

## Method selection

```text
1. Official JSON API?     → API connector or acquisition:"api" recipe
2. Sitemap of jobs?       → sitemap type (structured detail only)
3. Detail JSON-LD?        → schema_org / structured HTMLJSONLD
4. Stable HTML only?      → recipe (list + detail field maps)
5. None of the above?     → do not crawl until a recipe/spec exists
```

Never invent apply URLs from the board homepage. Never emit title-empty URL stubs.

## Go contracts

```go
// pkg/connectors
type Connector interface {
    Type() domain.SourceType
    Crawl(ctx, src) CrawlIterator
}
type CrawlIterator interface {
    Next(ctx) bool
    Items() []domain.ExternalOpportunity
    // …
}

// pkg/crawlaccept — single gate after extract
func Accept(Input) Result  // Accepted payload or Reject
```

## Seeds

`seeds/**/*.json` load at crawler boot into `sources`. Use structured `source_type` values that have connectors (see `apps/crawler/service/setup.go`). Hostile or unstructured boards should stay `status: "paused"` until a recipe/spec is ready.

## Trustage maintenance

| Workflow | Purpose |
|----------|---------|
| per-source crawl schedule | Dispatch crawl |
| source-crawl-overdue | Catch-up |
| crawl-runs-sweep | Resume/fail stuck runs |
| crawl-watchdog | Stall detection |
| sources-recipe-backfill | Queue recipe generation for HTML boards |

## Related

- [crawl-pipeline.md](./crawl-pipeline.md) — queue, worker, identity merge
- [first-deploy-runbook.md](./first-deploy-runbook.md)
- [capacity-planning.md](./capacity-planning.md)
