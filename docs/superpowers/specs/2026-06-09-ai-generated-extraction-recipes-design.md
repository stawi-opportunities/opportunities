# AI-Generated Per-Source Extraction Recipes

**Status:** Design approved — ready for implementation planning
**Date:** 2026-06-09
**Author:** Peter Bwire (with Claude)
**Scope:** Core recipe engine. Company-as-deduplicated-entity is an explicitly deferred fast-follow.

---

## 1. Problem & Goal

The crawler today runs an LLM call on essentially **every request** for any "universal"
source (brightermonday, jobberman, generic_html, schema_org, and newly-discovered unknown
HTML sites):

- `universal.DiscoverLinks()` — one LLM call per **listing** page to find detail URLs
  (`pkg/connectors/universal/universal.go:120`).
- `enrichOne()` → `Extractor.Extract()` — one LLM call per **detail** page to extract fields
  (`apps/crawler/service/crawl_request_handler.go:838-861`, call at `:852`).
- A classifier LLM call per page for multi-kind sources.

Per-source tuning is limited to `Source.ExtractionPromptExtension` (free text appended to the
prompt) — the model still runs on every page. This is the dominant inference cost and a
latency/throughput bottleneck (6-minute per-stub timeout, concurrency 4).

**Goal:** Use AI **once per source** to *learn how to extract* (the best selectors / data
paths / field mappings for greenhouse.com vs brightermonday.com), persist that as a
deterministic **recipe**, and run **LLM-free extraction** per page thereafter. Capture the
**company name, logo URL, and profile blurb** as part of extraction. Re-learn automatically
only when a recipe drifts.

The result moves AI cost from **per page** (thousands/day) to **per source, per generation
event** (rare, self-healing).

### Non-goals (deferred to fast-follow spec)
- Company as a deduplicated entity (dedup by domain, shared company profiles).
- Downloading and re-hosting logos. This spec extracts and carries the logo **URL**.

---

## 2. Design Decisions (locked)

| # | Decision | Choice |
|---|----------|--------|
| 1 | Scope | Core engine first; company name + logo URL + profile folded in as recipe fields. |
| 2 | Source coverage | One **unified** recipe model. Every source has a recipe, with acquisition modes `api` / `structured_data` / `selectors`. Official APIs are kept as the `api` mode — not downgraded to scraping. |
| 3 | Recipe format | **Structured-data-first, selector fallback** (prefer schema.org JSON-LD / `__NEXT_DATA__` / microdata; CSS/XPath only where needed). |
| 4 | Per-page extraction | Purely deterministic — **no LLM at crawl time**. |
| 5 | Recovery on drift | **Auto-regenerate** from fresh samples + re-verify; operator review only as a backstop if regeneration keeps failing. |
| 6 | Kind resolution | **Deterministic, never a per-page classifier.** Resolve kind from a structured field/URL pattern learned at generation. If a source mixes kinds with no deterministic rule, split it into per-kind sub-sources. |
| 7 | Recipe storage | Dedicated `sources.extraction_recipe` column + a `source_recipes` history table (versioning + rollback). Not the reserved `Config` blob. |
| 8 | Multi-kind | Kind-agnostic by construction, driven by `opportunity.Registry` (`job`, `scholarship`, `tender`, `deal`, `funding`). |

---

## 3. Architecture & Recipe Lifecycle

New package `pkg/recipe` with three independently-testable units:

- **`Recipe`** — the persisted data structure (§4).
- **`Executor`** — deterministic, no AI. Turns a page/record into `ExternalOpportunity`s.
  Replaces the universal connector's AI `DiscoverLinks()` + per-stub `Extract()`.
- **`Generator`** — AI-backed, runs *occasionally*. Synthesizes/repairs a recipe from sample
  pages using the existing inference client (`pkg/extraction`).

The deterministic half (`pagecontext`, `fieldeval`, `executor`) has **no dependency on the LLM
client** — that isolation is what makes steady-state crawling AI-free and the executor unit
testable against HTML fixtures.

**Lifecycle per source:**

1. **Onboard** (admin-create or `sources.discovered.v1`) → status `verifying`.
2. **Generate** — `Generator` fetches a listing page + N detail samples, harvests embedded
   structured data (reusing the JSON-LD/`__NEXT_DATA__` harvest at `extractor.go:161-183`), and
   asks the LLM to emit a `Recipe` targeting the kind contract(s) from `opportunity.Registry`.
3. **Validation gate** — dry-run the `Executor` on the archived samples; run the existing
   `opportunity.Verify()` on outputs. Accept only if the sample pass-rate clears
   `RECIPE_PASS_THRESHOLD`; otherwise repair/retry (bounded), else → operator.
4. **Activate** — persist recipe (version N), source → `active`.
5. **Execute (steady state)** — every crawl runs `Executor` only. Zero LLM calls. Emits
   opportunities incl. company name + logo URL + profile.
6. **Drift detect** — reuse the health signals in `page_completed_handler.go:61-136`
   (reject_rate / health_score / `needs_tuning`). When reject-rate or missing-required-field
   rate over a window crosses `RECIPE_REGEN_REJECT_RATE` → mark recipe **stale** → emit
   `recipe.regenerate.v1`.
7. **Regenerate** — re-run steps 2-3 on fresh samples and **atomically swap** (the old recipe
   keeps serving until the new one passes the gate). After `RECIPE_MAX_REGEN_FAILURES`
   consecutive failures → `needs_tuning` + operator review.

---

## 4. Recipe Schema

The schema is built on one reusable primitive, the **`FieldExtractor`**, which is where
"structured-data-first, selector fallback" lives: an ordered list of sources, first non-empty
wins.

```go
// FieldExtractor: how to pull ONE value from a page/record.
// From is tried in order — structured data first, selectors last.
type FieldExtractor struct {
    From      []string // ["json_ld","next_data","microdata","selector","meta","const"]
    JSONPath  string   // for json_ld / next_data / api records
    Microdata string   // schema.org itemprop
    Selector  string   // CSS selector
    Attr      string   // attribute to read (default: text); e.g. "href","src","content"
    Meta      string   // <meta name|property> (e.g. "og:image")
    Const     string   // literal fallback
    Transform []string // ordered allowlist: "trim","absolute_url","html_to_text",
                        //   "parse_date","parse_money","lower","collapse_ws",...
    Required  bool
}

type Recipe struct {
    Version      int
    GeneratedAt  time.Time
    Model        string   // inference model that synthesized it
    SampleURLs   []string // pages it was learned/validated against
    SampleHashes []string // content hashes — detect when samples are stale
    PassRate     float64  // sample Verify() pass-rate at the validation gate

    Acquisition string   // "api" | "structured_data" | "selectors"
    Kind        KindRule
    List        ListRule   // enumerate detail URLs/records (replaces AI DiscoverLinks)
    Detail      DetailRule // extract one opportunity        (replaces per-page LLM Extract)
}

type KindRule struct {
    Mode  string         // "source_default" | "fixed" | "by_path"
    Fixed string         // when Mode=="fixed"
    Path  FieldExtractor // when Mode=="by_path" (deterministic: structured field / URL pattern)
}

type ListRule struct {
    Mode string // "api" | "sitemap" | "structured_data" | "selector"

    // api mode: the list response often already carries full records.
    Endpoint  string
    Method    string
    Params    map[string]string
    ItemsPath string // JSON path to the array of records

    // html modes:
    ItemSelector string         // each card/row on a listing page
    Link         FieldExtractor // → absolute detail URL

    Pagination Pagination
}

type Pagination struct {
    Mode     string         // "none" | "page_param" | "cursor" | "next_link"
    Param    string         // page_param: ?page=N
    Cursor   FieldExtractor // cursor: where the next cursor lives
    Next     FieldExtractor // next_link: the "next page" URL
    MaxPages int
}

type DetailRule struct {
    RecordSource string // "api" | "json_ld" | "next_data" | "microdata" | "html"

    // Universal envelope (names mirror opportunity.Verify requirements)
    Title         FieldExtractor
    Description   FieldExtractor // Verify needs >= 50 chars
    IssuingEntity FieldExtractor // == company name
    ApplyURL      FieldExtractor
    LocationText  FieldExtractor
    AnchorCountry FieldExtractor // falls back to source.Country (existing chain preserved)
    Remote        FieldExtractor
    PostedAt      FieldExtractor
    Deadline      FieldExtractor
    AmountMin     FieldExtractor
    AmountMax     FieldExtractor
    Currency      FieldExtractor
    Categories    FieldExtractor // yields a list

    // Company / logo — folded in (URL only; hosting+dedup is the fast-follow)
    CompanyLogoURL FieldExtractor // json_ld hiringOrganization.logo -> microdata -> og:image
    CompanyProfile FieldExtractor // employer "about" blurb

    // Kind-specific fields → ExternalOpportunity.Attributes (keyed by the kind's attr names)
    Attributes map[string]FieldExtractor
}
```

**Concrete shapes produced:**

- **Greenhouse (`api`):** `List{Mode:"api", Endpoint:"/v1/boards/{co}/jobs?content=true",
  ItemsPath:"jobs"}`, `Detail{RecordSource:"api", Title:{From:["json_ld"],JSONPath:"title"}, ...}`
  — one fetch, no second request, no scraping. The API connector becomes *expressible as a
  recipe* rather than hand-coded.
- **BrighterMonday (`structured_data`/`selectors`):** `List{Mode:"selector",
  ItemSelector:".job-card", Link:{Selector:"a.job-link",Attr:"href",Transform:["absolute_url"]},
  Pagination:{Mode:"next_link", Next:{Selector:"a[rel=next]",Attr:"href"}}}`, then
  `Detail{RecordSource:"json_ld", Title:{From:["json_ld","selector"], JSONPath:"title",
  Selector:"h1"}, CompanyLogoURL:{From:["json_ld","meta"],
  JSONPath:"hiringOrganization.logo", Meta:"og:image"}, ...}`.

---

## 5. Components

### Package layout
```
pkg/recipe/
  recipe.go       // Recipe, FieldExtractor, ListRule, DetailRule, KindRule + Validate()
  pagecontext.go  // PageContext: unified view over a page/record
  fieldeval.go    // FieldExtractor evaluation engine + transform registry
  executor.go     // deterministic Executor (api + html drivers) -> []ExternalOpportunity
  generator.go    // AI Generator: sample -> prompt -> parse -> repair loop
  validator.go    // dry-run + opportunity.Verify() gate -> RecipeValidationReport
  store.go        // RecipeRepository (datastore.BaseRepository) + atomic swap/rollback
  prompts.go      // recipe-synthesis prompt templates
```

### A. `PageContext` + `FieldExtractor` engine (deterministic core)

`PageContext` normalizes the four data planes a page exposes so `FieldExtractor.From` can try
them in order:

```go
type PageContext struct {
    URL      string
    HTML     *goquery.Document  // parsed DOM (selector + microdata)
    JSONLD   []map[string]any   // all <script type="application/ld+json"> blocks
    NextData map[string]any     // __NEXT_DATA__ / __NUXT__ / inline state
    Meta     map[string]string  // <meta name|property> -> content
    Record   map[string]any     // present in api mode (the JSON record itself)
}
```

The harvesting that populates `JSONLD`/`NextData` exists today in `extractor.go:161-183`; it is
**lifted into `pkg/content`** so the Generator and Executor share one harvester — identical
behavior at learn-time and run-time (critical: the recipe is validated against the same harvest
it will run on).

`fieldeval.Evaluate(fx, pc)` walks `fx.From`, resolves the first non-empty value, then pipes it
through `fx.Transform`. The **transform registry** is `map[string]func(string)(string,error)` —
a fixed allowlist of pure functions, extensible and kind-agnostic. Unknown transform names are
rejected at `recipe.Validate()`.

### B. `Executor` (deterministic; replaces both AI call sites)

Implements the existing `connectors.Connector` interface (`connector.go:44-52`), so it drops
into the registry with **zero handler changes** — `crawl_request_handler.go` keeps calling
`connector.Crawl(ctx, source)`. Two acquisition drivers behind one interface:

- **`apiDriver`** (`Acquisition=="api"`): builds requests from `ListRule`, parses `ItemsPath`
  → records; detail fields read straight from each record — usually **one fetch, no detail
  round-trip**. This is how greenhouse/workday/JSON boards become recipes without losing their
  API.
- **`htmlDriver`** (`structured_data`/`selectors`): fetch listing via existing `PageFetcher` →
  `PageContext` → enumerate `ItemSelector`+`Link` → follow `Pagination`. For each detail URL,
  fetch → `PageContext` → run `DetailRule`. Concurrency/politeness reuse the existing `httpx`
  client and `EnrichConcurrency` semaphore; R2 archival (`resolveArchiveRef`) is unchanged.

Output: `[]ExternalOpportunity` assembled field-by-field, `Kind` set by `KindRule`,
`AnchorLocation.Country` falling back to `source.Country` (existing chain at
`crawl_request_handler.go:399-417` preserved). The Executor **stops at producing
`ExternalOpportunity`** — the existing `Verify → Normalize → pipeline_variants →
IngestedQueue` path runs exactly as today.

Precise swap-out:

| Today (per page, AI) | After (per page, deterministic) |
|---|---|
| `universal.DiscoverLinks()` LLM call | `ListRule` enumeration |
| `enrichOne()` → `Extractor.Extract()` LLM call | `DetailRule` + `FieldExtractor` engine |
| classifier LLM call (multi-kind) | `KindRule.by_path` |

### C. `Generator` (AI; per-source/occasional)

1. **Sample** — listing page (BaseURL or first sitemap entry) + `RECIPE_SAMPLE_COUNT` (default
   4) detail pages spread across the listing. Fetch via `PageFetcher`, archive to R2, record
   content hashes.
2. **Build generation context** — per sample: truncated HTML + **full harvested
   JSON-LD/`__NEXT_DATA__`** (highest signal) + meta. Plus, from `opportunity.Registry`, the
   **target schema for each `source.Kinds`** (`Spec.ExtractionPrompt` + required attributes).
   This is what makes generation correct for scholarships/tenders/deals — the generator is told
   the kind's contract, not a hardcoded job shape.
3. **Synthesize** — one LLM call (reusing `pkg/extraction` client, `response_format:
   json_object`) asking for a `Recipe` that prefers structured-data paths, uses selectors only
   where needed, and identifies list/pagination/kind rules.
4. **Parse + schema-validate** — `recipe.Validate()` checks structural integrity (every
   required envelope field has an extractor, transforms exist in the allowlist, selectors
   parse).
5. **Repair loop** — on schema-invalid or validation-gate failure, feed the concrete errors
   back to the LLM, bounded by `RECIPE_MAX_GEN_ATTEMPTS` (default 3). After that → operator
   backstop.

The Generator is the **only** AI consumer, running at onboarding + on drift — not per page.

### D. `Validator` (quality gate)

Dry-runs the `Executor` against the **archived sample HTML** (offline, deterministic), runs
`opportunity.Verify()` on every output, computes a pass-rate. Accepts only if pass-rate ≥
`RECIPE_PASS_THRESHOLD` (default 0.8) and all universal-required fields resolve on every
sample. Emits a `RecipeValidationReport` (mirrors `Source.VerificationReport`) recording
per-field hit rates and per-sample Verify results — reusable in the admin UI.

### E. `Store` / versioning / atomic swap

- New `sources.extraction_recipe jsonb` column = the **active** recipe.
- New **`source_recipes` history table**: `(id, source_id, version, recipe jsonb, status
  [active|superseded|rejected], pass_rate, model, validation_report jsonb, created_at)`.
- **Atomic swap** (one transaction): insert version N+1 as `active`, mark prior `superseded`,
  update `sources.extraction_recipe` + version. The **old recipe keeps serving until the new
  one passes the gate** — generation failure never takes a source down. Rollback =
  re-activate a prior `source_recipes` row. Repository follows `datastore.BaseRepository`.

### F. Integration & control flow (Frame events)

- **Onboarding/verification** — `source_discovered_handler` / admin-create sets `verifying` and
  emits **`recipe.generate.v1`**. New `RecipeGenerateHandler` runs Generator→Validator→Store; on
  pass → `active`, on exhaustion → `needs_tuning`.
- **Crawl** — connector resolution at `crawl_request_handler.go:222` picks the `Executor` when
  `source.extraction_recipe` is present; otherwise the legacy path (enables gradual rollout).
- **Drift** — extend `page_completed_handler.go:61-136`: when reject-rate or missing-required-
  field rate crosses `RECIPE_REGEN_REJECT_RATE` for a recipe-driven source → mark recipe stale
  → emit **`recipe.regenerate.v1`**.
- **Regenerate** — `RecipeRegenerateHandler`: same flow, atomic-swap on success; after
  `RECIPE_MAX_REGEN_FAILURES` consecutive failures → `needs_tuning` + operator. Emits
  `recipe.activated.v1` / `recipe.rejected.v1` for observability.

### G. Multi-kind generalization (built in)

Every kind-specific decision routes through `opportunity.Registry`:
- **Generation** reads `Spec.ExtractionPrompt` + required attributes per `source.Kinds`.
- **`DetailRule.Attributes`** is `map[string]FieldExtractor` keyed by the kind's attribute
  names — no job assumptions.
- **`Verify()`** is already kind-aware, so the validation gate is correct for all kinds for
  free.
- **Multi-kind sources** resolve kind via `KindRule.by_path` (a structured field or URL pattern
  learned at generation). If kinds can't be split deterministically, generation flags the
  source for splitting into per-kind sub-sources — never a per-page classifier.

**Caveat:** scholarship/tender/deal sites embed schema.org structured data less often than job
boards, so those recipes lean more on the selector fallback and may regenerate a bit more often
until they stabilize. The architecture handles it.

### H. Config & wiring (Frame)

New knobs (reusing the existing `INFERENCE_*` client), following the pattern in
`apps/crawler/config/config.go`:

| Env | Default | Purpose |
|-----|---------|---------|
| `RECIPE_ENABLED` | false | Global kill-switch (+ per-source rollout flag) |
| `RECIPE_SAMPLE_COUNT` | 4 | Detail samples per generation |
| `RECIPE_PASS_THRESHOLD` | 0.8 | Min sample Verify() pass-rate to accept a recipe |
| `RECIPE_MAX_GEN_ATTEMPTS` | 3 | Generation/repair attempts before operator |
| `RECIPE_REGEN_REJECT_RATE` | 0.5 | Windowed reject-rate that triggers regeneration |
| `RECIPE_REGEN_MIN_PAGES` | 20 | Don't regenerate on tiny samples |
| `RECIPE_MAX_REGEN_FAILURES` | 3 | Consecutive regen failures before operator |

---

## 6. Error Handling & Safety

- **Generation fails / inference down** → `recipe.generate.v1` is retryable (Frame redelivery);
  the source keeps its current recipe and stays live. Operator only after attempt/ failure caps.
- **A page won't extract** → incomplete record → existing `Verify()` rejects it down the current
  dead-letter path; counts toward drift. No crashes, no special-casing.
- **Runaway crawl** → `Pagination.MaxPages` + per-source page caps; existing politeness/health
  reconciliation unchanged.
- **Safety (important):** the AI emits **declarative data only** (selectors, JSON paths,
  transform *names*) — never code. The transform registry is a fixed allowlist; unknown names
  are rejected at `Validate()`. Acquisition endpoints are constrained to the source's own host
  (SSRF guard); `apply_url` is output data, never fetched by the executor. "AI-generated
  scraper" therefore carries no code-execution risk.
- **Resource limits:** per-page size caps and generation token/size caps reuse the existing
  truncation in `pkg/extraction`.

---

## 7. Observability (OpenTelemetry via Frame)

- Headline metric proving the win: **`llm_calls_total` collapses** from per-page to
  per-generation-event.
- `recipes_generated_total{kind,result}`, `recipe_pass_rate`,
  `recipe_regenerations_total{reason,result}`, and per-field **hit-rate gauges**
  (`executor_field_hit{kind,field}`) — the early-warning signal that a selector drifted before
  reject-rate spikes.
- Structured logs via `util.Log(ctx)` carrying source id + recipe version.
- Admin surfaces (reusing `RecipeValidationReport`): per-field hit rates, **diff between recipe
  versions**, manual **"regenerate now"** / **"roll back to version N"** actions.

---

## 8. Testing Strategy (per `testing-go`)

- **Unit:** transforms (table-driven); `FieldExtractor.From` ordering; `PageContext` harvesting
  from HTML fixtures (JSON-LD/`__NEXT_DATA__`/microdata/meta); `recipe.Validate()`.
- **Golden recipes:** checked-in sample pages for **one of each kind** (job = greenhouse +
  brightermonday, scholarship, tender, deal) with expected extracted opportunities — regression
  guard proving execution correctness and kind-agnosticism.
- **Generator:** a **fake LLM** returning canned recipe JSON (valid + malformed→repair) tests
  prompt assembly and the parse/repair loop — no real inference in CI.
- **Integration (testcontainers Postgres, `BaseTestSuite`):** `RecipeRepository` CRUD, atomic
  swap, rollback, history; end-to-end `generate event → fake LLM → active recipe`;
  drift→regenerate. Race detection on.

---

## 9. Rollout / Migration (no big-bang)

- **Phase 0:** ship dormant behind `RECIPE_ENABLED=false`; migrations add the column + history
  table (backward compatible — sources without a recipe keep the legacy path).
- **Phase 1 — shadow mode:** for a few pilot sources, generate a recipe and run the `Executor`
  **in parallel** with the current AI path, logging output diffs but emitting from the old path.
  Proves quality at zero risk.
- **Phase 2 — cutover:** flip pilot sources to recipe-authoritative (AI path off) via the
  per-source flag; watch reject_rate/health and the `llm_calls` drop.
- **Phase 3 — backfill:** one-off job emits `recipe.generate.v1` for all existing universal
  sources; express API/structured connectors as `api`-mode recipes opportunistically (identical
  behavior, low risk).
- **Phase 4 — retire** the per-page `DiscoverLinks`/`Extract` path once coverage is high; keep
  the Generator + operator backstop.

---

## 10. Affected / New Code (orientation for planning)

**New:** `pkg/recipe/*` (8 files above); migrations for `sources.extraction_recipe` +
`source_recipes`; `RecipeGenerateHandler` / `RecipeRegenerateHandler` in
`apps/crawler/service/`; recipe config block in `apps/crawler/config/config.go`.

**Modified:** lift structured-data harvester from `pkg/extraction/extractor.go:161-183` into
`pkg/content`; connector resolution in `apps/crawler/service/crawl_request_handler.go:222`;
drift trigger in `apps/crawler/service/page_completed_handler.go`; source onboarding in
`apps/crawler/service/source_discovered_handler.go`; connector registry in
`apps/crawler/service/setup.go`.

**Reused unchanged:** `opportunity.Verify()` / `Registry` / `Spec`; `Normalizer`;
`pipeline_variants` ledger; R2 archive; `httpx`; `pkg/extraction` inference client.

---

## 11. Open Questions for Implementation Planning

- Exact JSON-path dialect for `FieldExtractor.JSONPath` (pick one library; must handle
  schema.org `@graph` arrays and nested `hiringOrganization`).
- Whether `KindRule.by_path` needs a small expression form (URL regex vs field-equals) or just
  a `FieldExtractor` + value map.
- Shadow-mode diff storage location (ephemeral logs vs a temporary table) for Phase 1.
