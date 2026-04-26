# Opportunity Generification Design

**Date:** 2026-04-26
**Status:** Spec — awaiting user review before plan writing
**Goal:** Refactor the platform's domain model from job-only to a generic *opportunity* model that supports jobs, scholarships, tenders, deals, funding opportunities, and arbitrary future kinds added by dropping a YAML file.

---

## Why

The repository was already renamed `stawi.jobs → opportunities` and the data plane (Iceberg, Manticore, R2, Frame topics) is kind-neutral. The leak is concentrated in the **domain envelope** (`ExternalJob`, `VariantIngestedV1`, `CanonicalUpsertedV1`), the **extractor prompt**, and the **per-kind UI / matcher logic**. Extending the platform to a new opportunity kind today requires forking the pipeline. After this refactor it requires a YAML file plus a UI onboarding component.

A second motivation: source registration today doesn't declare what fields the source emits, so verification can't catch a connector that returns malformed scholarship data shaped like a job. After this refactor, every source declares its kind(s) and required attributes up-front, and verification rejects records that violate the declared contract.

---

## Architecture (north-star)

A single `Opportunity` envelope carries every kind. A string-typed `Kind` field discriminates. Kinds are loaded at boot from `definitions/opportunity-kinds/*.yaml` into a `Registry`. Each YAML declares:

- The display name and issuing-entity label
- The URL prefix used in Hugo, R2, and sitemaps
- Universal-required, kind-required, and kind-optional attributes
- The category taxonomy
- The Manticore facets to expose in the UI
- The LLM extraction prompt
- The onboarding flow ID and matcher ID

The registry mounts as a Kubernetes ConfigMap; adding a kind in production is `kubectl apply -k` + `kubectl rollout restart`. Five seed kinds ship in-tree: `job`, `scholarship`, `tender`, `deal`, `funding`.

One Iceberg schema, one Manticore index, one Frame topic family — sparse polymorphic columns rather than parallel pipelines. One matcher binary (`apps/matching/`) hosts a `Matcher` per kind; the kind boundary is logical, not infrastructural.

This is greenfield. There is no backfill from the old job-shaped schema; the old shape is removed in one commit.

---

## Domain model

### `pkg/opportunity/spec.go` (new)

```go
package opportunity

type Spec struct {
    Kind                     string
    DisplayName              string   // "Job" / "Scholarship" / ...
    IssuingEntityLabel       string   // "Company" / "Institution" / ...
    AmountKind               string   // "salary" | "stipend" | "budget" | "discount" | "grant" | ""
    URLPrefix                string   // "jobs" / "scholarships" — drives Hugo + R2 + sitemap
    UniversalRequired        []string // subset of {title, description, issuing_entity, apply_url, anchor_country, anchor_region, anchor_city}
    KindRequired             []string // attributes that must be present in opportunity.Attributes
    KindOptional             []string
    Categories               []string // valid values for opportunity.Categories
    SearchFacets             []string // sparse Manticore columns surfaced as UI filters
    ExtractionPrompt         string   // kind-specific LLM body (prepended with a shared universal prefix)
    OnboardingFlow           string   // matches a UI component ID
    Matcher                  string   // matcher implementation key inside apps/matching/
}
```

### YAML shape (operator-facing)

A Spec serialises as YAML for `definitions/opportunity-kinds/`:

```yaml
# definitions/opportunity-kinds/scholarship.yaml
kind: scholarship
display_name: Scholarship
issuing_entity_label: Institution
amount_kind: stipend
url_prefix: scholarships

# Each entry is a known envelope key:
#   title, description, issuing_entity, apply_url,
#   anchor_country, anchor_region, anchor_city.
# Scholarships are often global, so anchor_country is NOT required here.
universal_required: [title, description, issuing_entity]

kind_required:      [deadline, field_of_study]
kind_optional:      [degree_level, gpa_min, age_max, eligible_nationalities, study_location]
categories: [STEM, Humanities, Arts, Business, Medicine, Law]
search_facets: [field_of_study, degree_level]
extraction_prompt: |
  Extract scholarship details from the page. Output JSON with fields:
  title, description, issuing_entity (the awarding body), apply_url,
  anchor_country (where the scholarship is held; empty if global),
  deadline (RFC3339), field_of_study, degree_level (one of:
  undergraduate, masters, doctoral, postdoc), amount_min, amount_max,
  currency, eligible_nationalities (ISO 3166-1 alpha-2 codes; empty for any).
onboarding_flow: scholarship-onboarding-v1
matcher: scholarship-matcher-v1
```

The `job` YAML by contrast sets `universal_required: [title, description, issuing_entity, apply_url, anchor_country]` because every job posting we ingest is anchored to a country.

### `pkg/opportunity/registry.go` (new)

```go
type Registry struct{ specs map[string]Spec }

// LoadFromDir walks dir for *.yaml files and validates each Spec
// (URLPrefix matches /^[a-z][a-z0-9-]*$/, no duplicate kinds, etc).
func LoadFromDir(dir string) (*Registry, error)

func (r *Registry) Lookup(kind string) (Spec, bool)
func (r *Registry) Known() []string

// Resolve falls back to a permissive default Spec when kind is unknown.
// Used at the publish/index boundary so an in-flight record doesn't crash
// the pipeline if its kind YAML was temporarily disabled.
func (r *Registry) Resolve(kind string) Spec
```

### `pkg/domain/location.go` (new)

```go
type Location struct {
    Country string  // ISO 3166-1 alpha-2; optional
    Region  string  // ISO 3166-2 (e.g. "US-CA") or free-form; optional
    City    string  // optional
    Lat     float64 // 0 = unset
    Lon     float64 // 0 = unset
}
```

### `pkg/domain/models.go` — `ExternalJob` becomes `ExternalOpportunity`

```go
type ExternalOpportunity struct {
    // Discriminator
    Kind string

    // Source identity (universal)
    SourceID, ExternalID string

    // Universal core
    Title         string
    Description   string
    IssuingEntity string   // was Company
    ApplyURL      string

    // Universal location
    AnchorLocation *Location // optional; nil for global/remote-only opps
    Remote         bool
    GeoScope       string    // "global" | "regional" | "national" | "local" | ""

    // Universal time
    PostedAt time.Time
    Deadline *time.Time

    // Universal monetary (semantics determined by Spec.AmountKind)
    AmountMin, AmountMax float64
    Currency             string

    // Universal taxonomy (validated against Spec.Categories)
    Categories []string

    // Kind-specific extension. Keys must satisfy Spec.KindRequired
    // and may include Spec.KindOptional. Anything else is flagged
    // by Verify() but not rejected.
    Attributes map[string]any

    // Pipeline metadata (kind-agnostic; unchanged from today)
    RawHTML, RawHash, ContentMarkdown string
    Source                            SourceType
    QualityScore                      float64
}
```

The previous `ExternalJob` fields (`Company`, `RemoteType`, `EmploymentType`, `Seniority`, `SalaryMin`, `SalaryMax`, `Skills`, `Roles`, `Benefits`, `RoleScope`, `TeamSize`, `ReportsTo`, `Department`, `Industry`) move into `Attributes` keyed by the names declared in the `job` YAML.

### Event payloads

`pkg/events/v1/opportunities.go` (renamed from `jobs.go`):

```go
type VariantIngestedV1 struct {
    VariantID, SourceID, ExternalID, HardKey string

    // Discriminator + universal
    Kind          string
    Stage         string // "ingested" | "extracted" | "normalized" | "validated"
    Title         string
    IssuingEntity string
    AnchorCountry string  // denormalized for fast partition pruning; AnchorLocation lives in Attributes
    Remote        bool
    Currency      string
    AmountMin     float64
    AmountMax     float64

    // Polymorphic
    Attributes map[string]any

    ScrapedAt time.Time
}
```

`CanonicalUpsertedV1` follows the same shape (drop `Company`/`RemoteType`/`EmploymentType`/`Seniority`/`SalaryMin`/`SalaryMax`; add `Kind`/`IssuingEntity`/`AnchorLocation` columns + `Attributes`).

`pkg/events/v1/names.go` topic constants stay neutral; only the doc comments change.

### Slug

`pkg/domain/models.go:BuildSlug` dispatches on kind:

| Kind | Pattern |
|---|---|
| `job` | `<title>-at-<issuing_entity>-<hash>` |
| `scholarship` | `<title>-at-<issuing_entity>-<hash>` |
| `tender` | `<title>-from-<issuing_entity>-<hash>` |
| `deal` | `<title>-at-<issuing_entity>-<hash>` |
| `funding` | `<title>-from-<issuing_entity>-<hash>` |

The dispatch table is hardcoded for now; if it gets wider we move it into the YAML.

---

## Source contract & verification

### `pkg/domain/models.go` — extend `Source`

```go
type Source struct {
    // ... existing fields ...

    // Declared kinds this source emits. Required at registration time.
    // Validated against Registry.Known().
    Kinds []string

    // Optional per-kind override that tightens Spec.KindRequired.
    // Used when a specific portal is known to always carry an
    // attribute that the kind YAML marks optional.
    RequiredAttributesByKind map[string][]string
}
```

Sources are registered via `apps/seeds/` (Hugo-style YAML files in the repo) and via the `/_admin/sources` endpoint on the crawler. Both paths now require `kinds` and validate against the registry.

### `pkg/opportunity/verify.go` (new)

Single verification entrypoint, called between extraction and publish:

```go
type VerifyResult struct {
    OK       bool
    Missing  []string  // required-but-empty attributes
    Extra    []string  // attributes outside KindRequired ∪ KindOptional (warning only)
    Mismatch string    // non-empty if Source didn't declare this Kind
}

func Verify(opp *ExternalOpportunity, src *Source, reg *Registry) VerifyResult
```

Verify checks, in order:
1. `opp.Kind ∈ src.Kinds` (source contract)
2. Universal required: `Spec.UniversalRequired` keys non-empty on the envelope
3. Kind required: `Spec.KindRequired` keys present and non-empty in `Attributes`
4. Source override: `src.RequiredAttributesByKind[kind]` keys present
5. Stray attributes: keys not in `KindRequired ∪ KindOptional` → `Extra` (warn, not reject)

Verify replaces `pkg/quality/gate.go`. The old length and hash checks fold into Verify under universal-required.

A failed Verify routes the variant to a dead-letter Iceberg table (`opportunities.variants_rejected`) for operator inspection rather than silently dropping.

---

## Pipeline changes

| Concern | File | Change |
|---|---|---|
| Connector iface | `pkg/connectors/connector.go` | `CrawlIterator.Jobs()` → `Items()` returning `[]ExternalOpportunity`. Connectors that emit a single kind tag every record (`RemoteOK → "job"`); generic HTML/Sitemap connectors leave `Kind` empty for the extractor to classify. |
| Extractor | `pkg/extraction/extractor.go` | Two stages: (a) `pickKind` — only when source declares >1 kind or zero kinds. Returns one of `Registry.Known()`. (b) `extract` — calls the LLM with `universalPromptPrefix + Spec.ExtractionPrompt`. Output JSON deserialized into `ExternalOpportunity` with `Attributes` populated from kind-specific keys. |
| Normalize | `pkg/normalize/normalize.go` | `JobVariant` → `OpportunityVariant`. Kind-specific normalisation rules dispatch on `opp.Kind`. |
| Verify | new: `pkg/opportunity/verify.go` | Replaces `pkg/quality/gate.go`. |
| Publish | `pkg/publish/r2.go` | R2 path becomes `<URLPrefix>/<slug>.json`. URL prefix from `Registry.Resolve(opp.Kind).URLPrefix`. |
| KV rebuild | `apps/worker/service/kv_rebuild.go` | Iterates `Registry.Known()` and lists each `<URLPrefix>/` prefix from R2 instead of hardcoded `jobs/`. |

### Geocoding

A new `pkg/geocode/` package wraps a bundled offline gazetteer (top ~10k cities). `Geocode(country, region, city)` returns `Location` with `Lat/Lon` populated when the city resolves; otherwise the location is returned with zero coords (still filterable by country/region). Geocoding runs in normalize.

---

## Storage layout

### R2

| Path | Was | Now |
|---|---|---|
| Canonical body | `opportunities-content/jobs/<slug>.json` | `opportunities-content/<URLPrefix>/<slug>.json` |
| Translation | `opportunities-content/jobs/<slug>/<lang>.json` | `opportunities-content/<URLPrefix>/<slug>/<lang>.json` |

### Iceberg

`opportunities.variants` schema (rewritten — greenfield):

```python
NestedField(1,  "variant_id",         StringType(),     required=True),
NestedField(2,  "source_id",          StringType(),     required=True),
NestedField(3,  "external_id",        StringType(),     required=True),
NestedField(4,  "hard_key",           StringType(),     required=True),
NestedField(5,  "kind",               StringType(),     required=True),
NestedField(6,  "stage",              StringType(),     required=True),
NestedField(7,  "title",              StringType(),     required=True),
NestedField(8,  "issuing_entity",     StringType(),     required=False),
NestedField(9,  "country",            StringType(),     required=False),
NestedField(10, "region",             StringType(),     required=False),
NestedField(11, "city",               StringType(),     required=False),
NestedField(12, "lat",                DoubleType(),     required=False),
NestedField(13, "lon",                DoubleType(),     required=False),
NestedField(14, "remote",             BooleanType(),    required=False),
NestedField(15, "geo_scope",          StringType(),     required=False),
NestedField(16, "currency",           StringType(),     required=False),
NestedField(17, "amount_min",         DoubleType(),     required=False),
NestedField(18, "amount_max",         DoubleType(),     required=False),
NestedField(19, "deadline",           TimestampType(),  required=False),
NestedField(20, "categories",         StringType(),     required=False), # comma-joined for Iceberg simplicity
NestedField(21, "attributes",         StringType(),     required=False), # JSON
NestedField(22, "scraped_at",         TimestampType(),  required=True),
```

`opportunities.embeddings` adds a `kind` column for filtered KNN. `opportunities.published` mirrors `variants` but only carries records that passed Verify.

A new `opportunities.variants_rejected` table mirrors `variants` and carries records that failed Verify, with an extra `reasons` column.

### Manticore

`pkg/searchindex/schema.go` — `idx_opportunities_rt` rewritten:

```sql
CREATE TABLE idx_opportunities_rt (
  -- Discriminator
  kind            string attribute,

  -- Universal indexable text
  title           text indexed,
  description     text indexed,
  issuing_entity  text indexed,
  categories      multi64,                      -- enumerated against Spec.Categories

  -- Universal location
  country         string attribute,
  region          string attribute,
  city            string attribute,
  lat             float attribute,
  lon             float attribute,
  remote          bool attribute,
  geo_scope       string attribute,

  -- Universal time
  posted_at       timestamp attribute,
  deadline        timestamp attribute,

  -- Universal monetary
  amount_min      float attribute,
  amount_max      float attribute,
  currency        string attribute,

  -- Sparse per-kind facet columns. Populated only when kind matches.
  -- Job-specific:
  employment_type    string attribute,
  seniority          string attribute,
  -- Scholarship-specific:
  field_of_study     string attribute,
  degree_level       string attribute,
  -- Tender-specific:
  procurement_domain string attribute,
  -- Funding-specific:
  funding_focus      string attribute,

  -- Embedding
  embedding          float_vector knn_type='hnsw' knn_dims='384' hnsw_similarity='cosine'
)
```

Schema migrations: `searchindex.Apply` is idempotent and applies `ALTER TABLE` for each new sparse column when the registry adds a kind. The migration is additive — existing rows keep their data.

---

## Matchers & onboarding

### `apps/matching/` (renamed from `apps/candidates/`)

```
apps/matching/
  cmd/main.go                       # one binary, one HelmRelease
  service/
    matchers/
      registry.go                   # registers all Matcher implementations at startup
      job/matcher.go                # CV ↔ job (the existing logic, scoped to kind=job)
      scholarship/matcher.go        # CV ↔ scholarship (filters on field_of_study, GPA, deadlines)
      tender/matcher.go             # company-profile ↔ tender (registered, ships disabled until pilot)
      deal/matcher.go               # browse-only stub
      funding/matcher.go            # org-profile ↔ funding (registered, ships disabled until pilot)
    profile/
      ...                           # candidate profile + OptIns persistence
```

Matcher interface:

```go
type Matcher interface {
    Kind() string
    SearchFilter(prefs json.RawMessage) (searchindex.Filter, error)
    Score(ctx context.Context, prefs json.RawMessage, opp *Opportunity) (float64, error)
}
```

The matcher router selects implementation by `opp.Kind` (or by the candidate's `OptIns` keys when retrieving from the search index).

### Preferences

`pkg/events/v1/candidates.go`:

```go
type PreferencesUpdatedV1 struct {
    CandidateID string
    OptIns      map[string]json.RawMessage // kind → kind-specific preferences blob
    UpdatedAt   time.Time
}
```

Each kind has a Go type for its preferences:

```go
// Embedded by every kind; rendered by the shared LocationPicker UI component.
type LocationPreference struct {
    Countries []string
    Regions   []string
    Cities    []string
    NearLat   float64
    NearLon   float64
    RadiusKm  int
    RemoteOK  bool
}

// pkg/matching/job/prefs.go
type JobPreferences struct {
    TargetRoles      []string
    EmploymentTypes  []string
    SeniorityLevels  []string
    SalaryMin        float64
    Currency         string
    Locations        LocationPreference
}

// pkg/matching/scholarship/prefs.go
type ScholarshipPreferences struct {
    DegreeLevels     []string
    FieldsOfStudy    []string
    Nationality      string   // ISO 3166-1 alpha-2
    GPAMin           float64
    Locations        LocationPreference  // study locations
    DeadlineWithin   time.Duration
}

// pkg/matching/tender/prefs.go
type TenderPreferences struct {
    CompanyName               string
    RegistrationCountry       string
    Capabilities              []string
    Certifications            []string
    BudgetCapacityMin         float64
    BudgetCapacityMax         float64
    ServiceCapabilityRegions  LocationPreference
}

// pkg/matching/deal/prefs.go
type DealPreferences struct {
    InterestCategories []string
    Countries          []string
}

// pkg/matching/funding/prefs.go
type FundingPreferences struct {
    OrganisationType         string  // "nonprofit" | "for_profit" | "individual"
    FocusAreas               []string
    GeographicScope          LocationPreference
    FundingAmountNeededMin   float64
    Currency                 string
}
```

### UI onboarding flows

```
ui/app/src/onboarding/
  shared/
    LocationPicker.tsx              # used by every flow
    AmountRange.tsx                 # used by job/scholarship/tender/funding
  job/
    v1/Flow.tsx                     # captures JobPreferences
  scholarship/
    v1/Flow.tsx                     # captures ScholarshipPreferences
  tender/
    v1/Flow.tsx
  deal/
    v1/Flow.tsx
  funding/
    v1/Flow.tsx
```

The onboarding router reads `Spec.OnboardingFlow` and dynamic-imports the corresponding component. Flows are versioned (`v1`, `v2`) so we can iterate without breaking saved profiles — old profiles continue rendering against the version they were captured under until the user re-onboards.

The candidate dashboard surfaces a **tab per opted-in kind**. A user may have all five tabs, or just one.

---

## UI changes

| File | Change |
|---|---|
| `ui/content/jobs/` | Move per kind: `ui/content/jobs/`, `ui/content/scholarships/`, `ui/content/tenders/`, etc. The Hugo path is the same as the URL prefix. |
| `ui/layouts/jobs/list.html` | Becomes `ui/layouts/<URLPrefix>/list.html` per kind, with a shared partial doing the universal envelope. |
| `ui/layouts/partials/job-card.html` | Renamed `opportunity-card.html`. Conditionally renders kind-specific badges (employment_type for jobs, deadline for scholarships, budget for tenders). |
| `ui/app/src/types/snapshot.ts` | `JobSnapshot` → `OpportunitySnapshot` with `kind` discriminator, `attributes` map, and `anchor_location` block. Type-narrowed accessors (`isJob(o)`, `isScholarship(o)`). |
| `ui/app/src/types/search.ts` | `Facets` becomes `{ universal: {...}, byKind: { job: {...}, scholarship: {...}, ... } }`. The UI renders facet groups based on which kinds appear in the result set. |
| `ui/app/src/components/JobDetail.tsx` | Splits into `OpportunityDetail.tsx` (router) + per-kind `JobBody.tsx`, `ScholarshipBody.tsx`, etc. |
| Sitemap | One sitemap shard per kind (`/sitemaps/jobs.xml`, `/sitemaps/scholarships.xml`, ...). |

---

## Telemetry

`pkg/telemetry/metrics.go`:

- Counter `pipeline.opportunities.ready` with `kind` attribute (was `pipeline.jobs.ready`).
- Histogram `pipeline.extraction.latency` with `kind` attribute.
- Counter `pipeline.verify.rejections` with `kind` and `reason` attributes.
- Gauge `registry.kinds_loaded` (set at boot from `Registry.Known()`).

Existing infrastructure-level alerts in `definitions/openobserve/alerts/` remain unchanged. Per-kind quality alerts are added later, after each kind has live signal.

---

## Migration phases

Eight phases, each independently shippable. Each phase produces a working, testable system.

### Phase 1 — Registry foundation

- `pkg/opportunity/{spec.go,registry.go,verify.go}` with seed YAMLs for all five kinds in `definitions/opportunity-kinds/`
- ConfigMap manifest mounted by every app
- Boot-time loader wired into each app's main; failure to load is fatal
- Unit tests covering registry parsing, validation, and `Resolve` fallback

No behaviour change — registry is defined but not yet consulted.

### Phase 2 — Domain rename

- `ExternalJob` → `ExternalOpportunity` with `Kind string` defaulting to `"job"` for in-flight code paths
- `pkg/domain/location.go` introduced; `Country/City/Region` strings on the old struct collapse into `Location`
- Tree-wide rename
- Old job-specific named fields (`Company`, `RemoteType`, etc.) move into `Attributes` keyed against the `job` YAML
- `BuildSlug` dispatches on kind

Tests pass; no wire-format change yet.

### Phase 3 — Event reshape (greenfield cut)

- New event payloads (`pkg/events/v1/opportunities.go`)
- Iceberg `opportunities.variants`, `opportunities.embeddings`, `opportunities.published`, `opportunities.variants_rejected` schemas rewritten
- Manticore `idx_opportunities_rt` rewritten with `kind` + sparse facet columns
- Materializer reads new event shape only

Old job-shaped Iceberg/Manticore artifacts dropped in the migration. The cluster runs against the fresh schema.

### Phase 4 — Source contract & verification

- `Source.Kinds` and `Source.RequiredAttributesByKind` added to the source schema
- `apps/seeds/` YAML files updated with `kinds` field
- Source-registration admin endpoint validates against the registry
- `pkg/opportunity/verify.go` wired between extract and publish
- Failed records routed to `opportunities.variants_rejected`
- `pkg/quality/gate.go` removed

### Phase 5 — Storage path & UI URL split

- R2 path becomes `<URLPrefix>/<slug>.json`
- KV rebuild iterates `Registry.Known()` and lists each prefix
- Hugo content reorganised into per-kind directories
- Per-kind sitemap shards
- Cloudflare 301s preserve any external bookmarks (not many — this is greenfield)

### Phase 6 — Extraction dispatch

- LLM extractor split into universal prefix + kind-specific body from registry
- Classifier stage added (skipped when source declares exactly one kind)
- Extraction prompt for each kind lives in its YAML
- Telemetry per-kind on extraction success/latency

### Phase 7 — Matchers + per-kind onboarding

- `apps/candidates/` renamed `apps/matching/`
- `Matcher` interface introduced; matchers registered for all five kinds (tender/funding stubs ship disabled)
- Preferences event becomes polymorphic (`OptIns map[kind]RawMessage`)
- UI onboarding flows per kind, versioned, with a shared `LocationPicker` component
- Dashboard tabs per opted-in kind

### Phase 8 — Pilot non-job kind

- Register a real scholarship source (e.g. DAAD or Chevening connector)
- End-to-end: source → crawler → extract → verify → publish → search → match
- Validates the architecture under live data and proves the registry/matcher loop

After Phase 8 the platform is production-ready for kind expansion. Adding a sixth kind (`internship`, `competition`, `event`, ...) becomes a YAML drop + matcher + onboarding flow.

---

## Out of scope

- Backfilling existing job records into the new shape — greenfield cut.
- Multi-language onboarding flows — defer to a separate i18n phase.
- Tender bidder matching against company-history embeddings — Phase 8 focuses on scholarships first; tender matcher ships as a stub and gets fleshed out in a follow-on plan.
- Deal/funding matchers beyond the stub — added when those kinds get registered with real sources.
- Watch-reload of the kind registry — operators restart pods to pick up changes; live-reload is gold-plating.

---

## Risks

1. **LLM classification accuracy.** A page that mixes job and scholarship language could classify wrong. Mitigation: source-level kind declaration is the strict path; the classifier only runs for sources that declare multiple kinds. Pilot with single-kind sources first.
2. **Sparse Manticore columns.** Manticore handles sparse attribute columns cheaply, but if the registry grows to 30+ kinds with 5+ facets each, that's 150 sparse columns. Acceptable up to ~20 kinds; revisit if we exceed that.
3. **Geocoding coverage gaps.** The bundled gazetteer only covers ~10k cities; rural sources will sometimes leave `Lat/Lon` zero and miss radius searches. Acceptable v1; revisit by adding a Nominatim fallback if it becomes a real complaint.
4. **Onboarding-flow drift.** Old saved preferences may render against a removed flow version. Mitigation: never delete a versioned flow component; add `v2`, leave `v1` mounted for legacy profiles. UI shows a one-time prompt encouraging re-onboarding when a new version exists.
5. **Matcher cardinality.** If kinds proliferate without retiring matchers, the single matcher binary's startup time grows. Acceptable for the foreseeable future; if it becomes a problem, split apps by kind grouping.

---

## Verification checklist (post-implementation)

- [ ] `pkg/opportunity` package with registry, verify, and tests
- [ ] Five seed YAMLs validate and load
- [ ] `ExternalOpportunity` carries `Kind` + `Attributes` + `AnchorLocation`
- [ ] Iceberg `opportunities.variants` schema reflects new shape
- [ ] Manticore `idx_opportunities_rt` reflects new shape
- [ ] Source registration rejects unknown kinds
- [ ] Verify rejects records that violate source contract
- [ ] R2 path is `<URLPrefix>/<slug>.json` for every published opportunity
- [ ] Hugo content tree has per-kind directories with per-kind URL prefixes
- [ ] Onboarding flow per kind, all five versioned at v1
- [ ] Matcher per kind registered (tender/deal/funding may ship disabled)
- [ ] Dashboard renders a tab per opted-in kind
- [ ] Telemetry counters carry `kind` attribute
- [ ] One non-job source registered, end-to-end through search and match
