# Crawl Framework — how a source gets crawled

The single rule this framework exists to enforce:

> **Onboarding a new source is a DATA change (a recipe), never a code change.**
> Go code exists only for the small, fixed set of *extraction categories*.
> If you find yourself writing a `pkg/connectors/<somesite>/` package, stop —
> that source is almost certainly one recipe away from working.

This document is the contract. It defines the two layers, the extraction
vocabulary, the method-selection decision tree, the Go interfaces, and the
exact onboarding workflow. Read it before adding any crawl code.

---

## 1. Two layers: generic engine (Go) vs. per-source recipe (data)

```
                    ┌─────────────────────────────────────────┐
   per source  →    │  RECIPE (JSON, stored on the source)    │   ← data, no deploy
                    │  picks an extractor + field mappings    │
                    └───────────────────┬─────────────────────┘
                                        │ configures
                    ┌───────────────────▼─────────────────────┐
   per category →   │  GENERIC EXTRACTOR (Go, pkg/recipe)      │   ← code, rarely changes
                    │  api · sitemap · html-listing           │
                    └─────────────────────────────────────────┘
```

There is **one generic extractor per *category of how a site exposes jobs*** —
not one per site. The categories are closed; adding a site never adds one.

| Category | Recipe selector | Engine path | Use when |
|---|---|---|---|
| **JSON API** | `acquisition: "api"` | `executor.apiPaged` | The board has an official/JSON endpoint returning postings (any ATS: Greenhouse, Lever, Workday; or a site's own `/api/jobs`). |
| **Sitemap** | `list.mode: "sitemap"` | `executor.sitemapPaged` | The site publishes a sitemap that enumerates job-detail URLs. Pulls the **whole** board, not just listing page 1. |
| **HTML listing → detail** | `list.mode: "selector"` + `link_pattern` | `executor.htmlPaged` | A listing page links to detail pages; extract each detail page. |

(There is also a parallel set of declarative **spec connectors** —
`pkg/connectors/spec/{jsonfeed,schemaorgjsonld,sitemap,rssfeed,xmlfeed,htmllisting}`
— loaded from YAML in R2. Same philosophy: generic impl + per-source config.
New work should prefer recipes; the spec connectors remain for sources already
configured that way.)

---

## 2. The extraction vocabulary — `FieldExtractor`

Every field (title, company, etc.) is pulled by a `FieldExtractor` that tries
data planes **in order, first non-empty wins**, then runs transforms. This is
the whole vocabulary — there is nothing site-specific in Go:

```json
{"from": ["json_ld","next_data","microdata","selector","xpath","meta","record","const","page_url"],
 "json_path": "$.title",        // json_ld / next_data / record
 "microdata": "title",          // schema.org itemprop
 "selector": "h1", "attr": "href",   // CSS
 "xpath": "//h1[@id='t']",      // XPath (antchfx/htmlquery)
 "meta": "og:title",            // <meta property/name>
 "const": "KE",                 // literal fallback
 "transform": ["trim","lower","collapse_ws","html_to_text","absolute_url","parse_money","parse_date"]}
```

Two engine behaviours make recipes robust and reusable:
- **JSON-LD `@id` resolution**: `hiringOrganization` is often a bare
  `{"@id":…}` pointing to a sibling `Organization` node; the engine resolves it
  so `$.hiringOrganization.name` works (standard schema.org, every compliant
  board).
- **`list.link_pattern`**: the URL substring that *defines a job*
  (`/listings/`, `/jobs/adverts/`). The engine harvests every same-host link
  containing it. The URL contract is far more stable than presentation classes
  (`data-cy`, hashed CSS modules) — prefer it over `item_selector`+`link`.

---

## 3. Method-selection decision tree — most FREE and ROBUST first

When onboarding a source, pick the **first** method that works:

```
1. Official JSON API?            → acquisition:"api"      (free, structured, complete)
       greenhouse-api / lever / workday-cxs / a site /api/jobs
2. Sitemap enumerating jobs?     → list.mode:"sitemap"    (free, complete board)
       /robots.txt → Sitemap:, or /sitemap.xml
3. Detail pages have JSON-LD?    → structured_data + link_pattern   (free, standard)
       schema.org JobPosting (Google-for-Jobs ⇒ near-universal)
4. Static HTML only?             → selector/xpath + link_pattern     (free, brittle-er)
5. JS-rendered, no sitemap/API?  → scrape.do render=true / headless  (PAID — last resort)
```

Never skip a free tier for a paid one. scrape.do (and `render`/`super`) costs
credits; browser-header emulation and the free tiers above handle the large
majority. See `docs/ops/crawl-pipeline.md` for the unblocker fallback order
(direct browser-emulated fetch → scrape.do → headless).

---

## 4. The interfaces (Go contracts)

Stable seams — implement against these, do not reach around them.

```go
// pkg/connectors — the connector contract (hand-coded connectors + the
// recipe adapter both satisfy it). One Connector per SourceType.
type Connector interface {
    Type() domain.SourceType
    Crawl(ctx, src) CrawlIterator
}
type CrawlIterator interface {           // page-at-a-time pull
    Next(ctx) bool
    Items() []domain.ExternalOpportunity
    RawPayload() []byte
    HTTPStatus() int
    Err() error
    Cursor() json.RawMessage
    Content() *content.Extracted
}

// pkg/recipe — the generic engine. Recipe sources route through this via
// the recipeconn adapter (a CrawlIterator wrapping an Executor).
type Fetcher interface { Get(ctx, url) (body []byte, status int, err error) }
func NewExecutor(*Recipe, Fetcher) *Executor
func (e *Executor) Page(ctx, src, PageState) (items, raw, status, next, done, err)
func (e *Executor) ListDetailURLs(ctx, src) ([]string, error)   // the source's job URLs
func (e *Executor) ListProbe(ctx, src) (int, error)             // count, for the gate

// pkg/recipe/recipecheck — the single verification authority. Everything
// (CLIs, fixture tests) calls this; nothing re-implements "is this correct".
func Check(ctx, Fetcher, *Registry, src, *Recipe, samples) Report
```

The HTTP layer (`pkg/connectors/httpx`) is shared by every method: browser
emulation, retry/backoff, compression, and the scrape.do/proxy unblocker
fallback. Connectors and the recipe `Fetcher` both use it, so anti-bot
handling is solved once.

---

## 5. Onboarding a source — the workflow

1. **Pick the method** (§3). Probe it: `curl robots.txt`/`sitemap.xml`, hit the
   ATS API, or check a detail page for `application/ld+json`.
2. **Record the definite listing**: set `sources.listing_path` (the exact jobs
   listing relative to `base_url`, or the sitemap/API URL). Never guessed — a
   homepage's prominent links are usually categories, not jobs.
3. **Author the recipe** — `cmd/recipe-gen` (LLM, follows §3 ordering) or by
   hand from the markup. Prefer `link_pattern` + standard JSON-LD paths.
4. **Verify against the live site**:
   - `go run ./cmd/recipe-verify -recipe r.json -base-url <url>` — runs the
     full gate (structural · detail extraction + `opportunity.Verify` · list
     rule · a real page-1 crawl). Must print `VERIFIED`.
   - Add a fixture under `tests/recipes/fixtures/<name>/` and
     `go test ./tests/recipes -update` to pin it (recorded pages → CI replays
     offline on every commit). Skip the fixture only for huge sitemaps; rely on
     `recipe-verify` there.
5. **Activate**: write the recipe to `source_recipes` (active) + mirror onto
   `sources.extraction_recipe`, clear `needs_tuning`.

Generated recipes follow the same gate automatically via the recipe-backfill
cron and the activation pass-rate threshold.

---

## 6. When Go code IS warranted (and when it is NOT)

**NOT** (these are recipes):
- A new job board, of any kind — API, sitemap, HTML, schema.org.
- A board on a known ATS (Greenhouse/Lever/Workday) — `acquisition:"api"`.
- A field that needs a different selector/path — change the recipe's
  `FieldExtractor`.

**YES** (rare, and reviewed):
- A genuinely new *extraction category* the engine can't express — e.g. a
  GraphQL-paginated API needing a new pagination mode, or a binary feed format.
  Add it as a generic mode in `pkg/recipe` (or a spec connector), driven by
  recipe config — never hardcode one site's fields.
- A new transform or `From` source in the vocabulary (§2).

**Deprecated**: the per-source hand-coded connectors
(`pkg/connectors/{greenhouse,workday,remoteok,jobicy,themuse,arbeitnow,
himalayas,smartrecruiters}`) predate this framework. They are all JSON APIs and
should migrate to `acquisition:"api"` recipes (as Lever already did). New
sources must not add to this list.
