# Opportunity Generification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refactor the platform from a job-only domain model to a generic Opportunity model supporting jobs, scholarships, tenders, deals, funding, and arbitrary future kinds added via YAML.

**Architecture:** A string-typed `Kind` discriminates a single `Opportunity` envelope. Specs load at boot from `definitions/opportunity-kinds/*.yaml` into a `Registry`. One Iceberg schema, one Manticore index with sparse per-kind columns, one matcher binary hosting per-kind `Matcher`s. Per-kind URL prefixes, per-kind onboarding flows, polymorphic preferences. Greenfield cut — no backfill.

**Tech Stack:** Go (Frame framework, iceberg-go v0.5.0, NATS JetStream, AWS SDK v2 for R2), Manticore Search, Apache Iceberg with JDBC catalog on Postgres, Cloudflare Pages (Hugo + Preact islands), OpenObserve OTEL, Trustage cron triggers, testcontainers-go.

**Spec:** `docs/superpowers/specs/2026-04-26-opportunity-generification-design.md`

**Execution note:** This plan has 8 phases and ~35 tasks. Phases are sequential — Phase N+1 depends on Phase N. After each phase commit, run the smoke check at the end of that phase before starting the next. Use a worktree for the whole plan (it spans every app).

---

## Phase 1 — Registry foundation

Outcome: `pkg/opportunity` package with five seed kinds loadable from `definitions/opportunity-kinds/`. No behaviour change yet — the registry is defined but not consulted by the pipeline.

### Task 1.1: Spec struct

**Files:**
- Create: `pkg/opportunity/spec.go`
- Test: `pkg/opportunity/spec_test.go`

- [ ] **Step 1: Write the failing test**

```go
// pkg/opportunity/spec_test.go
package opportunity

import "testing"

func TestSpec_RequireKind(t *testing.T) {
	s := Spec{}
	if err := s.Validate(); err == nil {
		t.Fatal("expected error for missing Kind")
	}
}

func TestSpec_RequireURLPrefix(t *testing.T) {
	s := Spec{Kind: "job", DisplayName: "Job"}
	if err := s.Validate(); err == nil {
		t.Fatal("expected error for missing URLPrefix")
	}
}

func TestSpec_URLPrefixFormat(t *testing.T) {
	for _, bad := range []string{"Jobs", "j obs", "jobs/", "JOBS"} {
		s := Spec{Kind: "job", DisplayName: "Job", URLPrefix: bad, IssuingEntityLabel: "Company"}
		if err := s.Validate(); err == nil {
			t.Errorf("expected error for url_prefix=%q", bad)
		}
	}
}

func TestSpec_ValidatePass(t *testing.T) {
	s := Spec{
		Kind:               "job",
		DisplayName:        "Job",
		IssuingEntityLabel: "Company",
		URLPrefix:          "jobs",
		UniversalRequired:  []string{"title", "description"},
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/opportunity/...`
Expected: build fails with `undefined: Spec`.

- [ ] **Step 3: Write minimal implementation**

```go
// pkg/opportunity/spec.go
// Package opportunity defines the kind registry that drives polymorphic
// behaviour across the platform: extraction prompts, verification rules,
// search facets, URL prefixes, onboarding flows, and matchers.
package opportunity

import (
	"fmt"
	"regexp"
)

// Spec is the contract a Kind imposes on the pipeline. It is the single
// source of truth: extraction reads ExtractionPrompt, verify reads
// UniversalRequired/KindRequired, search reads SearchFacets, the UI reads
// OnboardingFlow, the matcher router reads Matcher. Adding a kind means
// authoring one Spec — usually as YAML under definitions/opportunity-kinds/.
type Spec struct {
	Kind               string   `yaml:"kind"`
	DisplayName        string   `yaml:"display_name"`
	IssuingEntityLabel string   `yaml:"issuing_entity_label"`
	AmountKind         string   `yaml:"amount_kind"`
	URLPrefix          string   `yaml:"url_prefix"`
	UniversalRequired  []string `yaml:"universal_required"`
	KindRequired       []string `yaml:"kind_required"`
	KindOptional       []string `yaml:"kind_optional"`
	Categories         []string `yaml:"categories"`
	SearchFacets       []string `yaml:"search_facets"`
	ExtractionPrompt   string   `yaml:"extraction_prompt"`
	OnboardingFlow     string   `yaml:"onboarding_flow"`
	Matcher            string   `yaml:"matcher"`
}

var urlPrefixRE = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// universalKeys is the closed set of envelope fields that may appear in
// UniversalRequired. Anything else is a YAML authoring error.
var universalKeys = map[string]struct{}{
	"title":          {},
	"description":    {},
	"issuing_entity": {},
	"apply_url":      {},
	"anchor_country": {},
	"anchor_region":  {},
	"anchor_city":    {},
}

func (s Spec) Validate() error {
	if s.Kind == "" {
		return fmt.Errorf("spec: kind is required")
	}
	if s.DisplayName == "" {
		return fmt.Errorf("spec %q: display_name is required", s.Kind)
	}
	if s.IssuingEntityLabel == "" {
		return fmt.Errorf("spec %q: issuing_entity_label is required", s.Kind)
	}
	if !urlPrefixRE.MatchString(s.URLPrefix) {
		return fmt.Errorf("spec %q: url_prefix %q must match %s", s.Kind, s.URLPrefix, urlPrefixRE)
	}
	for _, k := range s.UniversalRequired {
		if _, ok := universalKeys[k]; !ok {
			return fmt.Errorf("spec %q: universal_required[%q] is not a known envelope key", s.Kind, k)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/opportunity/...`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add pkg/opportunity/spec.go pkg/opportunity/spec_test.go
git commit -m "feat(opportunity): Spec struct + validation"
```

### Task 1.2: Registry loader

**Files:**
- Create: `pkg/opportunity/registry.go`
- Test: `pkg/opportunity/registry_test.go`

- [ ] **Step 1: Write the failing test**

```go
// pkg/opportunity/registry_test.go
package opportunity

import (
	"os"
	"path/filepath"
	"testing"
)

func writeYAML(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadFromDir_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "job.yaml", `
kind: job
display_name: Job
issuing_entity_label: Company
url_prefix: jobs
universal_required: [title, description, issuing_entity, apply_url, anchor_country]
kind_required: [employment_type]
categories: [Programming, Design]
`)
	writeYAML(t, dir, "scholarship.yaml", `
kind: scholarship
display_name: Scholarship
issuing_entity_label: Institution
url_prefix: scholarships
universal_required: [title, description, issuing_entity]
kind_required: [deadline, field_of_study]
categories: [STEM, Arts]
`)

	reg, err := LoadFromDir(dir)
	if err != nil {
		t.Fatalf("LoadFromDir: %v", err)
	}
	if got := len(reg.Known()); got != 2 {
		t.Errorf("Known()=%d, want 2", got)
	}
	job, ok := reg.Lookup("job")
	if !ok || job.URLPrefix != "jobs" {
		t.Errorf("Lookup(job) = %+v, %v", job, ok)
	}
}

func TestLoadFromDir_DuplicateKindRejected(t *testing.T) {
	dir := t.TempDir()
	body := `
kind: job
display_name: Job
issuing_entity_label: Company
url_prefix: jobs
`
	writeYAML(t, dir, "a.yaml", body)
	writeYAML(t, dir, "b.yaml", body)
	if _, err := LoadFromDir(dir); err == nil {
		t.Fatal("expected duplicate-kind error")
	}
}

func TestLoadFromDir_DuplicateURLPrefixRejected(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "a.yaml", `
kind: job
display_name: Job
issuing_entity_label: Company
url_prefix: jobs
`)
	writeYAML(t, dir, "b.yaml", `
kind: opening
display_name: Opening
issuing_entity_label: Company
url_prefix: jobs
`)
	if _, err := LoadFromDir(dir); err == nil {
		t.Fatal("expected duplicate-url_prefix error")
	}
}

func TestLoadFromDir_InvalidYAMLRejected(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "broken.yaml", "kind: job\nurl_prefix: [")
	if _, err := LoadFromDir(dir); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestRegistry_ResolveFallback(t *testing.T) {
	reg := &Registry{specs: map[string]Spec{}}
	got := reg.Resolve("unknown")
	if got.Kind != "unknown" {
		t.Errorf("Resolve fallback Kind=%q, want %q", got.Kind, "unknown")
	}
	if got.URLPrefix != "unknown" {
		t.Errorf("Resolve fallback URLPrefix=%q, want %q", got.URLPrefix, "unknown")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/opportunity/...`
Expected: build fails (`LoadFromDir` undefined).

- [ ] **Step 3: Write minimal implementation**

```go
// pkg/opportunity/registry.go
package opportunity

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Registry holds the kinds loaded at boot.
type Registry struct {
	specs map[string]Spec
}

// LoadFromDir walks dir for *.yaml files, parses each as a Spec, validates,
// and returns a Registry. Duplicate kinds or duplicate url_prefixes are
// rejected. The order of files on disk does not matter.
func LoadFromDir(dir string) (*Registry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("opportunity: read %s: %w", dir, err)
	}
	specs := map[string]Spec{}
	prefixes := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("opportunity: read %s: %w", path, err)
		}
		var s Spec
		if err := yaml.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("opportunity: parse %s: %w", path, err)
		}
		if err := s.Validate(); err != nil {
			return nil, fmt.Errorf("opportunity: %s: %w", path, err)
		}
		if _, exists := specs[s.Kind]; exists {
			return nil, fmt.Errorf("opportunity: duplicate kind %q (in %s)", s.Kind, path)
		}
		if other, exists := prefixes[s.URLPrefix]; exists {
			return nil, fmt.Errorf("opportunity: duplicate url_prefix %q (kinds %q and %q)", s.URLPrefix, other, s.Kind)
		}
		specs[s.Kind] = s
		prefixes[s.URLPrefix] = s.Kind
	}
	return &Registry{specs: specs}, nil
}

// Lookup returns the Spec for kind and a boolean indicating presence.
func (r *Registry) Lookup(kind string) (Spec, bool) {
	s, ok := r.specs[kind]
	return s, ok
}

// Known returns the registered kinds in alphabetical order.
func (r *Registry) Known() []string {
	out := make([]string, 0, len(r.specs))
	for k := range r.specs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Resolve returns the Spec for kind, falling back to a permissive default
// when kind is unknown. Use this on the publish/index path so an in-flight
// record doesn't crash the pipeline if its kind YAML was disabled.
func (r *Registry) Resolve(kind string) Spec {
	if s, ok := r.specs[kind]; ok {
		return s
	}
	return Spec{Kind: kind, DisplayName: kind, URLPrefix: kind, IssuingEntityLabel: "Issuer"}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/opportunity/...`
Expected: PASS (5 tests in addition to spec tests).

- [ ] **Step 5: Commit**

```bash
git add pkg/opportunity/registry.go pkg/opportunity/registry_test.go
git commit -m "feat(opportunity): Registry loader from YAML directory"
```

### Task 1.3: Five seed YAMLs

**Files:**
- Create: `definitions/opportunity-kinds/job.yaml`
- Create: `definitions/opportunity-kinds/scholarship.yaml`
- Create: `definitions/opportunity-kinds/tender.yaml`
- Create: `definitions/opportunity-kinds/deal.yaml`
- Create: `definitions/opportunity-kinds/funding.yaml`
- Test: `pkg/opportunity/seeds_test.go`

- [ ] **Step 1: Write the failing test**

```go
// pkg/opportunity/seeds_test.go
package opportunity

import "testing"

func TestSeedKindsLoad(t *testing.T) {
	reg, err := LoadFromDir("../../definitions/opportunity-kinds")
	if err != nil {
		t.Fatalf("LoadFromDir: %v", err)
	}
	want := []string{"deal", "funding", "job", "scholarship", "tender"}
	got := reg.Known()
	if len(got) != len(want) {
		t.Fatalf("Known() = %v, want %v", got, want)
	}
	for i, k := range want {
		if got[i] != k {
			t.Errorf("Known()[%d] = %q, want %q", i, got[i], k)
		}
	}
	for _, k := range want {
		s, _ := reg.Lookup(k)
		if s.URLPrefix == "" || s.OnboardingFlow == "" || s.Matcher == "" {
			t.Errorf("kind %q: incomplete spec %+v", k, s)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/opportunity/...`
Expected: FAIL — `read ../../definitions/opportunity-kinds: no such file or directory`.

- [ ] **Step 3: Author the five YAMLs**

```yaml
# definitions/opportunity-kinds/job.yaml
kind: job
display_name: Job
issuing_entity_label: Company
amount_kind: salary
url_prefix: jobs
universal_required: [title, description, issuing_entity, apply_url, anchor_country]
kind_required: [employment_type]
kind_optional: [remote_type, seniority, skills, benefits, geo_restrictions, ats_platform]
categories: [Programming, Design, Marketing, Sales, Operations, Finance, HR, Customer-Support, Data, Product, Legal, Other]
search_facets: [employment_type, remote_type, seniority]
extraction_prompt: |
  Extract job posting details from the page. Output JSON with fields:
  title, description (markdown), issuing_entity (the hiring company),
  apply_url, anchor_country (ISO 3166-1 alpha-2 of hiring country),
  anchor_region, anchor_city, remote (bool), employment_type
  (one of: full-time, part-time, contract, internship, freelance),
  seniority (one of: intern, junior, mid, senior, lead, manager, director, executive),
  amount_min, amount_max, currency, skills (string array),
  geo_restrictions (e.g. "US-only", "EMEA", or empty for global).
onboarding_flow: job-onboarding-v1
matcher: job-matcher-v1
```

```yaml
# definitions/opportunity-kinds/scholarship.yaml
kind: scholarship
display_name: Scholarship
issuing_entity_label: Institution
amount_kind: stipend
url_prefix: scholarships
universal_required: [title, description, issuing_entity]
kind_required: [deadline, field_of_study]
kind_optional: [degree_level, gpa_min, age_max, eligible_nationalities, study_location]
categories: [STEM, Humanities, Arts, Business, Medicine, Law, Other]
search_facets: [field_of_study, degree_level]
extraction_prompt: |
  Extract scholarship details from the page. Output JSON with fields:
  title, description (markdown), issuing_entity (the awarding body),
  apply_url, anchor_country (ISO 3166-1 alpha-2 of where the scholarship
  is held; empty if global), deadline (RFC3339), field_of_study,
  degree_level (one of: undergraduate, masters, doctoral, postdoc),
  amount_min, amount_max, currency, gpa_min (float),
  eligible_nationalities (ISO 3166-1 alpha-2 codes; empty for any).
onboarding_flow: scholarship-onboarding-v1
matcher: scholarship-matcher-v1
```

```yaml
# definitions/opportunity-kinds/tender.yaml
kind: tender
display_name: Tender
issuing_entity_label: Issuing Agency
amount_kind: budget
url_prefix: tenders
universal_required: [title, description, issuing_entity, anchor_country]
kind_required: [deadline]
kind_optional: [procurement_domain, bidder_eligibility, certifications_required, service_areas]
categories: [Construction, IT, Consulting, Healthcare, Transport, Energy, Education, Other]
search_facets: [procurement_domain]
extraction_prompt: |
  Extract tender / RFP details from the page. Output JSON with fields:
  title, description (markdown), issuing_entity (the issuing agency),
  apply_url (submission portal), anchor_country, deadline (RFC3339),
  procurement_domain, amount_min, amount_max (contract budget range),
  currency, bidder_eligibility (free text), certifications_required
  (string array), service_areas (string array of countries/regions).
onboarding_flow: tender-onboarding-v1
matcher: tender-matcher-v1
```

```yaml
# definitions/opportunity-kinds/deal.yaml
kind: deal
display_name: Deal
issuing_entity_label: Merchant
amount_kind: discount
url_prefix: deals
universal_required: [title, description, issuing_entity, apply_url]
kind_required: [expiry]
kind_optional: [discount_percent, redemption_countries, coupon_code]
categories: [Software, Hardware, Travel, Education, Subscriptions, Other]
search_facets: [discount_percent]
extraction_prompt: |
  Extract deal / promotion details from the page. Output JSON with fields:
  title, description (markdown), issuing_entity (the merchant),
  apply_url (deal redemption URL), expiry (RFC3339), discount_percent,
  amount_min, amount_max (savings range when not a percent),
  currency, coupon_code, redemption_countries (ISO 3166-1 alpha-2 codes).
onboarding_flow: deal-onboarding-v1
matcher: deal-matcher-v1
```

```yaml
# definitions/opportunity-kinds/funding.yaml
kind: funding
display_name: Funding Opportunity
issuing_entity_label: Funder
amount_kind: grant
url_prefix: funding
universal_required: [title, description, issuing_entity]
kind_required: [deadline, focus_area]
kind_optional: [organisation_eligibility, target_regions, funding_amount_range]
categories: [Climate, Education, Health, Tech, Arts, Social-Enterprise, Other]
search_facets: [focus_area]
extraction_prompt: |
  Extract grant / funding opportunity details. Output JSON with fields:
  title, description (markdown), issuing_entity (the funder),
  apply_url, anchor_country (or empty for global funds),
  deadline (RFC3339), focus_area, amount_min, amount_max (grant
  amount range), currency, organisation_eligibility (one of:
  nonprofit, for_profit, individual, any), target_regions
  (ISO 3166-1 alpha-2 codes or region names; empty for global).
onboarding_flow: funding-onboarding-v1
matcher: funding-matcher-v1
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/opportunity/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add definitions/opportunity-kinds/ pkg/opportunity/seeds_test.go
git commit -m "feat(opportunity): five seed kind YAMLs (job, scholarship, tender, deal, funding)"
```

### Task 1.4: Boot-time loader integration

**Files:**
- Modify: `apps/crawler/cmd/main.go`
- Modify: `apps/api/cmd/main.go`
- Modify: `apps/candidates/cmd/main.go`
- Modify: `apps/worker/cmd/main.go`
- Modify: `apps/writer/cmd/main.go`
- Modify: `apps/materializer/cmd/main.go`

- [ ] **Step 1: Identify the kinds path config**

Add to each app's config struct (find the `Config` struct in `apps/<app>/config/config.go`):

```go
// OpportunityKindsDir is the directory that holds the opportunity-kinds YAML
// registry. Mounted as a ConfigMap in production at this path.
OpportunityKindsDir string `env:"OPPORTUNITY_KINDS_DIR" envDefault:"/etc/opportunity-kinds"`
```

- [ ] **Step 2: Wire boot-time loading**

In each `apps/<app>/cmd/main.go`, find where `cfg` has been populated and add (replace `<app>` with the actual app name in error context):

```go
reg, err := opportunity.LoadFromDir(cfg.OpportunityKindsDir)
if err != nil {
	util.Log(ctx).WithError(err).Fatal("opportunity registry: load failed")
}
util.Log(ctx).WithField("kinds", reg.Known()).Info("opportunity registry: loaded")
```

Add the import: `"github.com/stawi-opportunities/opportunities/pkg/opportunity"`.

- [ ] **Step 3: Pass the registry to handlers that will need it**

Pass `reg *opportunity.Registry` into:
- crawler service constructors (will be used by extractor + verify in later phases)
- materializer service constructors (will be used by URL prefix resolution)
- writer service constructors (will be used by R2 publisher)
- candidates service constructors (will be used by matcher router)

For Phase 1 the registry is wired but not yet consulted — handlers accept it and ignore it. Later phases activate it.

- [ ] **Step 4: Add the dev-time symlink for `go run` / tests**

Create `Makefile` target if not present, otherwise extend existing setup:

```makefile
# in Makefile under existing targets
opportunity-kinds-link:
	@mkdir -p /tmp/opportunity-kinds
	@cp -f definitions/opportunity-kinds/*.yaml /tmp/opportunity-kinds/
	@echo "Linked opportunity-kinds to /tmp/opportunity-kinds"
```

Then update `.env.example`:

```
OPPORTUNITY_KINDS_DIR=/tmp/opportunity-kinds
```

- [ ] **Step 5: Smoke test each app boots**

Run for each app:

```bash
make opportunity-kinds-link
OPPORTUNITY_KINDS_DIR=/tmp/opportunity-kinds go run ./apps/crawler/cmd 2>&1 | head -20
```

Expected: log line `"opportunity registry: loaded" kinds=[deal funding job scholarship tender]`. Kill after the registry log appears (the app continues to its normal boot).

Repeat for `api`, `candidates`, `worker`, `writer`, `materializer`.

- [ ] **Step 6: Commit**

```bash
git add apps/*/cmd/main.go apps/*/config/config.go Makefile .env.example
git commit -m "feat(opportunity): wire registry boot-time load into all apps"
```

### Task 1.5: ConfigMap manifest (deployments repo)

**Files:**
- Create (in deployments repo): `manifests/namespaces/opportunities/configmap-opportunity-kinds.yaml`
- Modify (in deployments repo): each HelmRelease's values to mount the ConfigMap at `/etc/opportunity-kinds`

This task lands in `/home/j/code/antinvestor/deployments/`, not the opportunities repo.

- [ ] **Step 1: Generate ConfigMap from current YAMLs**

```bash
# from /home/j/code/stawi.jobs:
kubectl create configmap opportunity-kinds \
  --from-file=definitions/opportunity-kinds/ \
  --dry-run=client -o yaml \
  --namespace=opportunities \
  > /tmp/configmap-opportunity-kinds.yaml
```

Move that file into the deployments repo at `manifests/namespaces/opportunities/configmap-opportunity-kinds.yaml`.

- [ ] **Step 2: Mount in each HelmRelease**

In every HelmRelease values block (api, candidates, crawler, materializer, worker, writer), add:

```yaml
extraVolumes:
  - name: opportunity-kinds
    configMap:
      name: opportunity-kinds
extraVolumeMounts:
  - name: opportunity-kinds
    mountPath: /etc/opportunity-kinds
    readOnly: true
env:
  OPPORTUNITY_KINDS_DIR: /etc/opportunity-kinds
```

- [ ] **Step 3: Commit deployments repo**

```bash
cd /home/j/code/antinvestor/deployments
git add manifests/namespaces/opportunities/
git commit -m "feat(opportunities): mount opportunity-kinds ConfigMap on every workload"
```

### Phase 1 smoke check

```bash
cd /home/j/code/stawi.jobs
go test ./pkg/opportunity/...
go build ./...
```

Expected: all tests pass, every app builds. The registry loads at boot but no behaviour changed.

---

## Phase 2 — Domain rename

Outcome: `ExternalJob` becomes `ExternalOpportunity` with a `Kind`, `Attributes` map, `AnchorLocation` block. Job-specific named fields move into `Attributes`. Connector interface renamed `Items()`. Tree-wide changes.

### Task 2.1: Location struct

**Files:**
- Create: `pkg/domain/location.go`
- Test: `pkg/domain/location_test.go`

- [ ] **Step 1: Write the failing test**

```go
// pkg/domain/location_test.go
package domain

import "testing"

func TestLocation_IsZero(t *testing.T) {
	if !(Location{}).IsZero() {
		t.Error("zero Location should report IsZero")
	}
	if (Location{Country: "KE"}).IsZero() {
		t.Error("Location with Country should not report IsZero")
	}
}

func TestLocation_HasCoords(t *testing.T) {
	if (Location{}).HasCoords() {
		t.Error("zero coords should not report HasCoords")
	}
	if !(Location{Lat: -1.286, Lon: 36.817}).HasCoords() {
		t.Error("Nairobi coords should report HasCoords")
	}
	if (Location{Lat: 0, Lon: 0}).HasCoords() {
		t.Error("0,0 should be treated as unset")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/domain/...`
Expected: build fails (`Location` undefined).

- [ ] **Step 3: Write minimal implementation**

```go
// pkg/domain/location.go
package domain

// Location is the universal geographic block carried on every Opportunity.
// Each field is optional; a fully-zero Location is valid and means
// "no anchor — opportunity is global / location-irrelevant".
type Location struct {
	Country string  `json:"country,omitempty"` // ISO 3166-1 alpha-2
	Region  string  `json:"region,omitempty"`  // ISO 3166-2 or free-form
	City    string  `json:"city,omitempty"`
	Lat     float64 `json:"lat,omitempty"` // 0 = unset
	Lon     float64 `json:"lon,omitempty"` // 0 = unset
}

// IsZero reports whether every field is empty.
func (l Location) IsZero() bool {
	return l.Country == "" && l.Region == "" && l.City == "" && l.Lat == 0 && l.Lon == 0
}

// HasCoords reports whether Lat/Lon are populated. (0,0) is treated as
// unset — there is no real-world opportunity at the equator/prime
// meridian intersection in our domain.
func (l Location) HasCoords() bool { return l.Lat != 0 && l.Lon != 0 }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/domain/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/domain/location.go pkg/domain/location_test.go
git commit -m "feat(domain): Location struct (universal geographic block)"
```

### Task 2.2: ExternalOpportunity (rename ExternalJob)

**Files:**
- Modify: `pkg/domain/models.go` (replace `ExternalJob` with `ExternalOpportunity`)
- Modify: every callsite returning/consuming `ExternalJob`

- [ ] **Step 1: Inventory callsites**

Run:

```bash
grep -rn "ExternalJob" --include='*.go' | tee /tmp/externaljob-callsites.txt
wc -l /tmp/externaljob-callsites.txt
```

Expected: ~30-50 callsites across `pkg/connectors/`, `pkg/extraction/`, `pkg/normalize/`, `apps/crawler/`.

- [ ] **Step 2: Rewrite `pkg/domain/models.go`**

Replace the existing `ExternalJob` block (lines around 142–189) with:

```go
// ExternalOpportunity is the canonical intermediate representation a
// connector returns for a single ingested item. Replaces the old
// ExternalJob; the kind discriminator decides which Spec governs
// extraction, verification, and downstream rendering.
type ExternalOpportunity struct {
	// Discriminator. Empty when the source declares zero kinds — the
	// extractor classifies before downstream stages run.
	Kind string `json:"kind,omitempty"`

	// Source identity
	SourceID   string `json:"source_id"`
	ExternalID string `json:"external_id"`

	// Universal core
	Title         string `json:"title"`
	Description   string `json:"description"`
	IssuingEntity string `json:"issuing_entity"`
	ApplyURL      string `json:"apply_url"`

	// Universal location
	AnchorLocation *Location `json:"anchor_location,omitempty"`
	Remote         bool      `json:"remote,omitempty"`
	GeoScope       string    `json:"geo_scope,omitempty"` // "global" | "regional" | "national" | "local" | ""

	// Universal time
	PostedAt time.Time  `json:"posted_at"`
	Deadline *time.Time `json:"deadline,omitempty"`

	// Universal monetary (semantics determined by Spec.AmountKind)
	AmountMin float64 `json:"amount_min,omitempty"`
	AmountMax float64 `json:"amount_max,omitempty"`
	Currency  string  `json:"currency,omitempty"`

	// Universal taxonomy (validated against Spec.Categories)
	Categories []string `json:"categories,omitempty"`

	// Kind-specific extension. Keys must satisfy Spec.KindRequired and
	// may include Spec.KindOptional. Anything else is flagged by Verify
	// (warning) but not rejected.
	Attributes map[string]any `json:"attributes,omitempty"`

	// Pipeline metadata (kind-agnostic)
	RawHTML         string     `json:"raw_html,omitempty"`
	RawHash         string     `json:"raw_hash,omitempty"`
	ContentMarkdown string     `json:"content_markdown,omitempty"`
	Source          SourceType `json:"source"`
	QualityScore    float64    `json:"quality_score,omitempty"`
}
```

- [ ] **Step 3: Tree-wide rename of identifier**

```bash
# Replace type name only — gofmt doesn't touch comments.
grep -rl "ExternalJob" --include='*.go' | xargs sed -i 's/ExternalJob/ExternalOpportunity/g'
```

Then build to find callers using removed fields:

```bash
go build ./... 2>&1 | tee /tmp/build-errors.txt
```

- [ ] **Step 4: Repair callers using removed fields**

Common breakages from the removed named fields. Apply each:

| Old expression | New expression |
|---|---|
| `j.Company` | `j.IssuingEntity` |
| `j.RemoteType` | `j.Attributes["remote_type"]` (string assertion) |
| `j.EmploymentType` | `j.Attributes["employment_type"]` |
| `j.Seniority` | `j.Attributes["seniority"]` |
| `j.SalaryMin` | `j.AmountMin` |
| `j.SalaryMax` | `j.AmountMax` |
| `j.Country` (top-level) | `j.AnchorLocation.Country` (with nil-check) |
| `j.City` (top-level) | `j.AnchorLocation.City` |
| `j.Region` (top-level) | `j.AnchorLocation.Region` |
| `j.Skills` | `j.Attributes["skills"]` |
| `j.Benefits` | `j.Attributes["benefits"]` |
| `j.Roles` | `j.Attributes["roles"]` |

For Attribute getters, add a helper to `pkg/domain/models.go`:

```go
// AttrString returns Attributes[key] as a string, or "" if missing/wrong type.
func (o ExternalOpportunity) AttrString(key string) string {
	if o.Attributes == nil {
		return ""
	}
	if v, ok := o.Attributes[key].(string); ok {
		return v
	}
	return ""
}

// AttrStringSlice returns Attributes[key] as a []string, or nil.
func (o ExternalOpportunity) AttrStringSlice(key string) []string {
	if o.Attributes == nil {
		return nil
	}
	switch v := o.Attributes[key].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// AttrFloat returns Attributes[key] as a float64, or 0.
func (o ExternalOpportunity) AttrFloat(key string) float64 {
	if o.Attributes == nil {
		return 0
	}
	switch v := o.Attributes[key].(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
}
```

Then rewrite callers using the helpers (they're cleaner than ad-hoc type assertions).

- [ ] **Step 5: Iterate until build passes**

```bash
go build ./... 2>&1 | head -50
```

Repeat the repair loop until the build is clean.

- [ ] **Step 6: Run all tests**

```bash
go test ./...
```

Some tests will need their fixtures updated (e.g. `extraction/extractor_test.go` building an `ExternalJob`). Update those to populate `Attributes` for the kind being tested.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "refactor(domain): ExternalJob → ExternalOpportunity with Kind, Attributes, AnchorLocation"
```

### Task 2.3: Connector interface — Items()

**Files:**
- Modify: `pkg/connectors/connector.go`
- Modify: every connector under `pkg/connectors/`

- [ ] **Step 1: Rename `Jobs()` → `Items()` on the iterator**

In `pkg/connectors/connector.go`, find:

```go
type CrawlIterator interface {
	HasNext() bool
	Next(ctx context.Context) (...)
	Jobs() []domain.ExternalOpportunity   // current state after Phase 2.2
	...
}
```

Rename to:

```go
type CrawlIterator interface {
	HasNext() bool
	Next(ctx context.Context) (...)
	Items() []domain.ExternalOpportunity
	...
}
```

- [ ] **Step 2: Tree-wide rename of method**

```bash
grep -rl "\.Jobs()" --include='*.go' pkg/connectors/ apps/ | xargs sed -i 's/\.Jobs()/\.Items()/g'
# Also any callers that explicitly call CrawlIterator.Jobs():
grep -rn "Jobs() \[\]" --include='*.go'
```

Apply rename to remaining matches manually if any.

- [ ] **Step 3: Each connector tags Kind**

For every connector in `pkg/connectors/<source>/<source>.go`, when constructing each `ExternalOpportunity`, set `Kind: "job"` (these are all job sources at this point):

```go
out = append(out, domain.ExternalOpportunity{
	Kind:       "job",
	SourceID:   sourceID,
	ExternalID: extID,
	// ... existing fields ...
})
```

Run a build:

```bash
go build ./...
go test ./pkg/connectors/...
```

- [ ] **Step 4: Commit**

```bash
git add pkg/connectors/ apps/
git commit -m "refactor(connectors): CrawlIterator.Jobs() → Items(); connectors tag Kind"
```

### Task 2.4: BuildSlug per kind

**Files:**
- Modify: `pkg/domain/models.go` (`BuildSlug`)
- Test: `pkg/domain/models_slug_test.go`

- [ ] **Step 1: Write the failing test**

```go
// pkg/domain/models_slug_test.go
package domain

import "testing"

func TestBuildSlug_PerKind(t *testing.T) {
	cases := []struct {
		kind, title, issuer, hash, want string
	}{
		{"job", "Senior Go Engineer", "Acme", "ab12cd34", "senior-go-engineer-at-acme-ab12cd34"},
		{"scholarship", "MSc Climate Science", "ETH Zurich", "ab12cd34", "msc-climate-science-at-eth-zurich-ab12cd34"},
		{"tender", "School Construction", "Min of Education", "ab12cd34", "school-construction-from-min-of-education-ab12cd34"},
		{"deal", "20% off Notion", "Notion Labs", "ab12cd34", "20-off-notion-at-notion-labs-ab12cd34"},
		{"funding", "Climate Tech Grant", "Bezos Earth Fund", "ab12cd34", "climate-tech-grant-from-bezos-earth-fund-ab12cd34"},
	}
	for _, c := range cases {
		got := BuildSlug(c.kind, c.title, c.issuer, c.hash)
		if got != c.want {
			t.Errorf("BuildSlug(%q,%q,%q,%q) = %q, want %q", c.kind, c.title, c.issuer, c.hash, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/domain/...`
Expected: FAIL — current `BuildSlug` doesn't take kind, or it's job-shaped.

- [ ] **Step 3: Rewrite `BuildSlug`**

In `pkg/domain/models.go`:

```go
// BuildSlug returns the deterministic public slug for an opportunity.
// The connector word ("at" vs "from") depends on kind:
//   job, scholarship, deal → "{title}-at-{issuer}-{hash}"
//   tender, funding        → "{title}-from-{issuer}-{hash}"
// Unknown kinds fall back to "at".
func BuildSlug(kind, title, issuer, hash string) string {
	connector := "at"
	switch kind {
	case "tender", "funding":
		connector = "from"
	}
	return slugify(title) + "-" + connector + "-" + slugify(issuer) + "-" + hash
}
```

Keep the existing `slugify` helper (the function that lowercases + dashes input). If the file currently exports `BuildSlug` without a kind parameter, update every caller:

```bash
grep -rn "BuildSlug(" --include='*.go'
```

For each caller, pass `opp.Kind` as the first argument.

- [ ] **Step 4: Run all tests**

```bash
go test ./...
```

- [ ] **Step 5: Commit**

```bash
git add pkg/domain/models.go pkg/domain/models_slug_test.go
git commit -m "feat(domain): BuildSlug dispatches on kind (at/from)"
```

### Phase 2 smoke check

```bash
go build ./...
go test ./...
```

Expected: all green. The platform now uses `ExternalOpportunity` everywhere; connectors tag every record with `Kind: "job"` since they're all job sources. No wire format change yet (events still use the old shape).

---

## Phase 3 — Event reshape (greenfield cut)

Outcome: New event payloads, new Iceberg schemas, new Manticore schema. Old job-shaped artifacts dropped. The cluster runs against fresh schemas.

### Task 3.1: New event payloads

**Files:**
- Create: `pkg/events/v1/opportunities.go` (renamed from `jobs.go`)
- Modify: `pkg/events/v1/canonicals.go`
- Modify: `pkg/events/v1/pipeline.go`
- Test: `pkg/events/v1/opportunities_test.go`

- [ ] **Step 1: Move and rewrite the variant payload**

```bash
git mv pkg/events/v1/jobs.go pkg/events/v1/opportunities.go
```

Rewrite the contents of `pkg/events/v1/opportunities.go`:

```go
// Package v1 carries the wire-format event types. The reshape from job-only
// to polymorphic Opportunity moves kind-specific fields into Attributes and
// adds a Kind discriminator to every payload.
package v1

import "time"

// VariantIngestedV1 is the first event a record emits after a connector
// has produced it. It carries everything needed for partition pruning
// without unpacking Attributes.
type VariantIngestedV1 struct {
	VariantID  string `json:"variant_id"`
	SourceID   string `json:"source_id"`
	ExternalID string `json:"external_id"`
	HardKey    string `json:"hard_key"`

	Kind  string `json:"kind"`
	Stage string `json:"stage"` // "ingested" | "extracted" | "normalized" | "validated"

	// Universal envelope fields (denormalised for analytics queries that
	// don't want to JSON-decode Attributes).
	Title         string `json:"title"`
	IssuingEntity string `json:"issuing_entity,omitempty"`
	AnchorCountry string `json:"anchor_country,omitempty"` // ISO 3166-1 alpha-2
	AnchorRegion  string `json:"anchor_region,omitempty"`
	AnchorCity    string `json:"anchor_city,omitempty"`
	Remote        bool   `json:"remote,omitempty"`

	Currency  string  `json:"currency,omitempty"`
	AmountMin float64 `json:"amount_min,omitempty"`
	AmountMax float64 `json:"amount_max,omitempty"`

	// Polymorphic — JSON-encoded for transport, decoded by consumers
	// that need kind-specific fields.
	Attributes map[string]any `json:"attributes,omitempty"`

	ScrapedAt time.Time `json:"scraped_at"`
}
```

- [ ] **Step 2: Rewrite `CanonicalUpsertedV1`**

In `pkg/events/v1/canonicals.go`, replace the existing struct with:

```go
type CanonicalUpsertedV1 struct {
	OpportunityID string `json:"opportunity_id"` // was canonical_job_id
	Slug          string `json:"slug"`
	HardKey       string `json:"hard_key"`

	Kind          string `json:"kind"`
	Title         string `json:"title"`
	IssuingEntity string `json:"issuing_entity"`
	ApplyURL      string `json:"apply_url"`

	AnchorCountry string `json:"anchor_country,omitempty"`
	AnchorRegion  string `json:"anchor_region,omitempty"`
	AnchorCity    string `json:"anchor_city,omitempty"`
	Lat           float64 `json:"lat,omitempty"`
	Lon           float64 `json:"lon,omitempty"`
	Remote        bool   `json:"remote,omitempty"`
	GeoScope      string `json:"geo_scope,omitempty"`

	PostedAt time.Time  `json:"posted_at"`
	Deadline *time.Time `json:"deadline,omitempty"`

	Currency  string  `json:"currency,omitempty"`
	AmountMin float64 `json:"amount_min,omitempty"`
	AmountMax float64 `json:"amount_max,omitempty"`

	Categories []string       `json:"categories,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`

	UpsertedAt time.Time `json:"upserted_at"`
}
```

Also rewrite `CanonicalExpiredV1` to use `OpportunityID` instead of `CanonicalJobID`.

- [ ] **Step 3: Rewrite `pipeline.go`**

Replace the existing `VariantNormalizedV1` and `VariantValidatedV1` with the new shape (drop Company/RemoteType/EmploymentType/etc., promote AmountMin/Max/Currency/Kind/Attributes):

```go
type VariantNormalizedV1 struct {
	VariantID  string         `json:"variant_id"`
	HardKey    string         `json:"hard_key"`
	Kind       string         `json:"kind"`
	NormalizedAt time.Time    `json:"normalized_at"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

type VariantValidatedV1 struct {
	VariantID    string    `json:"variant_id"`
	HardKey      string    `json:"hard_key"`
	Kind         string    `json:"kind"`
	Valid        bool      `json:"valid"`
	Reasons      []string  `json:"reasons,omitempty"`
	ValidatedAt  time.Time `json:"validated_at"`
	QualityScore float64   `json:"quality_score,omitempty"`
}
```

- [ ] **Step 4: Update topic constant comments**

In `pkg/events/v1/names.go`, replace the comment block on lines 7–22 to refer to "Opportunity pipeline" instead of "Job pipeline". The constants themselves (`TopicVariantsIngested`, etc.) are unchanged.

- [ ] **Step 5: Add round-trip test**

```go
// pkg/events/v1/opportunities_test.go
package v1

import (
	"encoding/json"
	"testing"
	"time"
)

func TestVariantIngestedV1_RoundTrip(t *testing.T) {
	in := VariantIngestedV1{
		VariantID:     "var_1",
		SourceID:      "src_remoteok",
		ExternalID:    "abc",
		HardKey:       "src_remoteok|abc",
		Kind:          "job",
		Stage:         "ingested",
		Title:         "Senior Go Engineer",
		IssuingEntity: "Acme",
		AnchorCountry: "KE",
		AnchorCity:    "Nairobi",
		Currency:      "USD",
		AmountMin:     90000,
		AmountMax:     130000,
		Attributes:    map[string]any{"employment_type": "full-time", "seniority": "senior"},
		ScrapedAt:     time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC),
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out VariantIngestedV1
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Kind != "job" || out.Attributes["employment_type"] != "full-time" {
		t.Fatalf("round-trip lost data: %+v", out)
	}
}
```

- [ ] **Step 6: Run tests + build**

```bash
go test ./pkg/events/v1/...
go build ./...
```

The build will fail in handlers that read removed fields (e.g. `materializer/service/`, `apps/writer/service/`). Repair them by reading from `Attributes` or the new universal fields. Iterate until clean.

- [ ] **Step 7: Commit**

```bash
git add pkg/events/v1/ apps/ pkg/
git commit -m "refactor(events/v1): polymorphic Opportunity payloads (Kind + Attributes)"
```

### Task 3.2: Iceberg schema rewrite

**Files:**
- Modify: `definitions/iceberg/_schemas.py`
- Modify: `scripts/bootstrap/create-iceberg.sh` (if it references the old schema)

- [ ] **Step 1: Write the new VARIANTS schema**

In `definitions/iceberg/_schemas.py`, replace the existing `VARIANTS` Schema block with:

```python
VARIANTS = Schema(
    NestedField(1,  "variant_id",     StringType(),    required=True),
    NestedField(2,  "source_id",      StringType(),    required=True),
    NestedField(3,  "external_id",    StringType(),    required=True),
    NestedField(4,  "hard_key",       StringType(),    required=True),
    NestedField(5,  "kind",           StringType(),    required=True),
    NestedField(6,  "stage",          StringType(),    required=True),
    NestedField(7,  "title",          StringType(),    required=True),
    NestedField(8,  "issuing_entity", StringType(),    required=False),
    NestedField(9,  "country",        StringType(),    required=False),
    NestedField(10, "region",         StringType(),    required=False),
    NestedField(11, "city",           StringType(),    required=False),
    NestedField(12, "lat",            DoubleType(),    required=False),
    NestedField(13, "lon",            DoubleType(),    required=False),
    NestedField(14, "remote",         BooleanType(),   required=False),
    NestedField(15, "geo_scope",      StringType(),    required=False),
    NestedField(16, "currency",       StringType(),    required=False),
    NestedField(17, "amount_min",     DoubleType(),    required=False),
    NestedField(18, "amount_max",     DoubleType(),    required=False),
    NestedField(19, "deadline",       TimestampType(), required=False),
    NestedField(20, "categories",     StringType(),    required=False),  # comma-joined
    NestedField(21, "attributes",     StringType(),    required=False),  # JSON
    NestedField(22, "scraped_at",     TimestampType(), required=True),
)
```

- [ ] **Step 2: Add VARIANTS_REJECTED schema**

```python
VARIANTS_REJECTED = Schema(
    NestedField(1, "variant_id",  StringType(),    required=True),
    NestedField(2, "source_id",   StringType(),    required=True),
    NestedField(3, "kind",        StringType(),    required=True),
    NestedField(4, "title",       StringType(),    required=True),
    NestedField(5, "reasons",     StringType(),    required=True),  # JSON array
    NestedField(6, "rejected_at", TimestampType(), required=True),
)
```

- [ ] **Step 3: Update PUBLISHED + EMBEDDINGS schemas**

`PUBLISHED` mirrors VARIANTS (same column shape; only records that passed Verify). `EMBEDDINGS` adds `kind` as a required column right after `variant_id`:

```python
EMBEDDINGS = Schema(
    NestedField(1, "variant_id", StringType(),    required=True),
    NestedField(2, "kind",       StringType(),    required=True),
    NestedField(3, "vector",     ListType(element_type=DoubleType()), required=True),
    NestedField(4, "embedded_at",TimestampType(), required=True),
)
```

- [ ] **Step 4: Update the table-creation script**

In `scripts/bootstrap/create-iceberg.sh`, ensure it creates `opportunities.variants_rejected` alongside the existing `opportunities.variants`. Confirm `opportunities.candidates_*` tables (CV pipeline) are unchanged — they belong to a different namespace.

- [ ] **Step 5: Update `pkg/icebergclient/tables.go`**

Add `variants_rejected` to `AppendOnlyTables`:

```go
var AppendOnlyTables = [][]string{
	{"opportunities", "variants"},
	{"opportunities", "variants_rejected"}, // NEW
	{"opportunities", "embeddings"},
	{"opportunities", "published"},
	{"opportunities", "crawl_page_completed"},
	{"opportunities", "sources_discovered"},
	{"candidates", "cv_uploaded"},
	{"candidates", "cv_extracted"},
	{"candidates", "cv_improved"},
	{"candidates", "preferences"},
	{"candidates", "embeddings"},
	{"candidates", "matches_ready"},
}
```

- [ ] **Step 6: Run integration tests**

```bash
go test -tags integration ./pkg/searchindex/... ./pkg/icebergclient/... 2>&1 | tail -20
```

- [ ] **Step 7: Commit**

```bash
git add definitions/iceberg/_schemas.py scripts/bootstrap/create-iceberg.sh pkg/icebergclient/tables.go
git commit -m "refactor(iceberg): polymorphic Opportunity schema; +variants_rejected"
```

### Task 3.3: Manticore schema rewrite

**Files:**
- Modify: `pkg/searchindex/schema.go`
- Modify: `pkg/searchindex/schema_test.go`

- [ ] **Step 1: Update the integration test to assert new columns**

```go
// pkg/searchindex/schema_test.go — append assertions
func TestApplySchema_HasKindColumn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	url, stop := startManticore(t, ctx)
	defer stop()
	client, _ := searchindex.Open(searchindex.Config{URL: url})
	defer client.Close()
	_ = client.Ping(ctx)
	if err := searchindex.Apply(ctx, client); err != nil {
		t.Fatalf("apply: %v", err)
	}
	raw, err := client.SQL(ctx, "DESCRIBE idx_opportunities_rt")
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	body := string(raw)
	for _, col := range []string{"kind", "issuing_entity", "country", "region", "city", "lat", "lon", "remote", "amount_min", "amount_max", "currency"} {
		if !strings.Contains(body, col) {
			t.Errorf("expected column %q in idx_opportunities_rt, got:\n%s", col, body)
		}
	}
}
```

- [ ] **Step 2: Rewrite the DDL constant**

In `pkg/searchindex/schema.go`, replace the `idxJobsRTDDL` constant with:

```go
const idxOpportunitiesRTDDL = `CREATE TABLE idx_opportunities_rt (
  -- Discriminator
  kind            string attribute,

  -- Universal indexable text
  title           text indexed,
  description     text indexed,
  issuing_entity  text indexed,
  categories      multi64,

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

  -- Sparse per-kind facet columns
  employment_type    string attribute,
  seniority          string attribute,
  field_of_study     string attribute,
  degree_level       string attribute,
  procurement_domain string attribute,
  funding_focus      string attribute,
  discount_percent   float attribute,

  -- Embedding
  embedding          float_vector knn_type='hnsw' knn_dims='384' hnsw_similarity='cosine'
)`

// Apply makes the index exist. Idempotent — repeated calls only ADD missing
// columns, never drop. New sparse facet columns added by future kind YAMLs
// land via ALTER TABLE in this same function.
func Apply(ctx context.Context, c *Client) error {
	_, err := c.SQL(ctx, idxOpportunitiesRTDDL)
	if err != nil && !isAlreadyExists(err) {
		return err
	}
	// Future: ALTER TABLE statements for newly-introduced sparse columns
	// based on the kind registry. For now, the seed kinds' columns are all
	// in idxOpportunitiesRTDDL.
	return nil
}
```

- [ ] **Step 3: Update the indexer's INSERT/UPDATE statement**

In `pkg/searchindex/indexer.go` (or whichever file builds the row), the column list and value list must match the new DDL. Locate the existing INSERT and replace its column list. Search for `INSERT INTO idx_opportunities_rt`:

```bash
grep -rn "INSERT INTO idx_opportunities_rt" --include='*.go'
```

Update the column list to match the DDL and unpack `Attributes` into the right sparse columns by checking `kind`. Helper:

```go
func sparseColsForKind(opp *domain.ExternalOpportunity) (cols []string, vals []any) {
	switch opp.Kind {
	case "job":
		cols = []string{"employment_type", "seniority"}
		vals = []any{opp.AttrString("employment_type"), opp.AttrString("seniority")}
	case "scholarship":
		cols = []string{"field_of_study", "degree_level"}
		vals = []any{opp.AttrString("field_of_study"), opp.AttrString("degree_level")}
	case "tender":
		cols = []string{"procurement_domain"}
		vals = []any{opp.AttrString("procurement_domain")}
	case "deal":
		cols = []string{"discount_percent"}
		vals = []any{opp.AttrFloat("discount_percent")}
	case "funding":
		cols = []string{"funding_focus"}
		vals = []any{opp.AttrString("funding_focus")}
	}
	return
}
```

- [ ] **Step 4: Run integration test**

```bash
go test -tags integration ./pkg/searchindex/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/searchindex/
git commit -m "refactor(searchindex): polymorphic idx_opportunities_rt with kind + sparse facets"
```

### Phase 3 smoke check

```bash
go build ./...
go test ./...
go test -tags integration ./pkg/searchindex/... ./pkg/eventlog/...
```

Expected: green. The platform now uses the new event/Iceberg/Manticore shape end-to-end.

---

## Phase 4 — Source contract & verification

Outcome: Sources declare which kinds they emit + per-kind required attributes. `Verify()` between extract and publish enforces the contract. Records that fail Verify go to `opportunities.variants_rejected`. `pkg/quality/gate.go` is removed.

### Task 4.1: Source.Kinds + RequiredAttributesByKind

**Files:**
- Modify: `pkg/domain/models.go` (`Source`)
- Modify: `pkg/repository/source.go` (DB schema migration)
- Modify: `apps/seeds/*.yaml`

- [ ] **Step 1: Extend Source struct**

In `pkg/domain/models.go`, add to the `Source` struct:

```go
// Kinds declares which opportunity kinds this source emits. Required at
// registration time; validated against the registry. A connector that emits
// only one kind always tags every record with that kind; multi-kind
// connectors (generic HTML, sitemap) leave Kind empty for the classifier.
Kinds []string `json:"kinds" db:"kinds"`

// RequiredAttributesByKind tightens Spec.KindRequired per source. Used
// when a specific portal is known to always carry an attribute that the
// kind YAML marks optional. Map from kind → list of attribute keys.
RequiredAttributesByKind map[string][]string `json:"required_attributes_by_kind" db:"required_attributes_by_kind"`
```

- [ ] **Step 2: Add migration**

In `pkg/repository/migrations/`, add `2026-04-26-source-kinds.sql`:

```sql
ALTER TABLE sources
  ADD COLUMN kinds TEXT[] NOT NULL DEFAULT ARRAY['job']::TEXT[],
  ADD COLUMN required_attributes_by_kind JSONB NOT NULL DEFAULT '{}'::JSONB;
```

The default `ARRAY['job']` seeds existing rows safely — every source is currently a job source.

- [ ] **Step 3: Update repository read/write**

In `pkg/repository/source.go`, ensure SELECT statements pull the new columns and INSERT/UPDATE statements set them. For tag-array round-trip with `pgx`, use `pq.StringArray` or the `pgtype.TextArray` type.

- [ ] **Step 4: Update seed YAMLs**

In `apps/seeds/`, every existing seed YAML gets `kinds: [job]` added. Bulk:

```bash
cd apps/seeds
for f in *.yaml; do
  if ! grep -q '^kinds:' "$f"; then
    sed -i '1i kinds: [job]' "$f"
  fi
done
```

Verify by reading a few files.

- [ ] **Step 5: Source-registration admin endpoint validates kinds**

Find the source-registration handler (search for `register source` or look in `apps/crawler/service/admin/`). Add validation:

```go
for _, k := range src.Kinds {
	if _, ok := reg.Lookup(k); !ok {
		return fmt.Errorf("source %q declares unknown kind %q (known: %v)", src.ID, k, reg.Known())
	}
}
```

- [ ] **Step 6: Run repository tests**

```bash
go test -tags integration ./pkg/repository/...
```

- [ ] **Step 7: Commit**

```bash
git add pkg/domain/models.go pkg/repository/ apps/seeds/ apps/crawler/
git commit -m "feat(domain): Source.Kinds + RequiredAttributesByKind, validated against registry"
```

### Task 4.2: Verify function

**Files:**
- Create: `pkg/opportunity/verify.go`
- Test: `pkg/opportunity/verify_test.go`

- [ ] **Step 1: Write the failing test**

```go
// pkg/opportunity/verify_test.go
package opportunity

import (
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

func newRegistry(t *testing.T) *Registry {
	t.Helper()
	reg, err := LoadFromDir("../../definitions/opportunity-kinds")
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestVerify_MissingTitle(t *testing.T) {
	reg := newRegistry(t)
	src := &domain.Source{Kinds: []string{"job"}}
	opp := &domain.ExternalOpportunity{Kind: "job", Description: "long enough description here for sure"}
	r := Verify(opp, src, reg)
	if r.OK || !contains(r.Missing, "title") {
		t.Fatalf("expected Missing to include 'title', got %+v", r)
	}
}

func TestVerify_KindNotInSourceContract(t *testing.T) {
	reg := newRegistry(t)
	src := &domain.Source{Kinds: []string{"job"}}
	opp := &domain.ExternalOpportunity{
		Kind:          "scholarship",
		Title:         "MSc Climate Science",
		Description:   "Long description that is well above the minimum length required for verify to be happy.",
		IssuingEntity: "ETH",
		Attributes:    map[string]any{"deadline": "2026-12-01", "field_of_study": "Climate"},
	}
	r := Verify(opp, src, reg)
	if r.OK || r.Mismatch == "" {
		t.Fatalf("expected Mismatch, got %+v", r)
	}
}

func TestVerify_KindRequiredAttributeMissing(t *testing.T) {
	reg := newRegistry(t)
	src := &domain.Source{Kinds: []string{"scholarship"}}
	opp := &domain.ExternalOpportunity{
		Kind:          "scholarship",
		Title:         "MSc Climate Science",
		Description:   "Long description that is well above the minimum length required for verify to be happy.",
		IssuingEntity: "ETH",
		// Missing deadline + field_of_study
	}
	r := Verify(opp, src, reg)
	if r.OK {
		t.Fatal("expected fail")
	}
	if !contains(r.Missing, "deadline") || !contains(r.Missing, "field_of_study") {
		t.Fatalf("expected Missing to include deadline+field_of_study, got %+v", r.Missing)
	}
}

func TestVerify_HappyPath(t *testing.T) {
	reg := newRegistry(t)
	src := &domain.Source{Kinds: []string{"job"}}
	opp := &domain.ExternalOpportunity{
		Kind:           "job",
		Title:          "Senior Go Engineer",
		Description:    "Long description that is well above the minimum length required for verify to be happy.",
		IssuingEntity:  "Acme",
		ApplyURL:       "https://acme.example/apply",
		AnchorLocation: &domain.Location{Country: "KE"},
		Attributes:     map[string]any{"employment_type": "full-time"},
	}
	r := Verify(opp, src, reg)
	if !r.OK {
		t.Fatalf("expected OK, got %+v", r)
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/opportunity/...`
Expected: build fails — `Verify` undefined.

- [ ] **Step 3: Implement Verify**

```go
// pkg/opportunity/verify.go
package opportunity

import (
	"fmt"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// VerifyResult is the outcome of Verify. OK is true when the record satisfies
// the source contract and the kind's universal+kind-required attributes.
// Extra is informational — keys outside KindRequired ∪ KindOptional are
// surfaced but do not fail the record.
type VerifyResult struct {
	OK       bool
	Missing  []string // required-but-empty fields/attributes
	Extra    []string // attributes outside KindRequired ∪ KindOptional
	Mismatch string   // non-empty if Source did not declare this Kind
}

// Verify runs the full contract check between an extracted record and the
// source that produced it. The order of checks matters: source contract
// (Mismatch) is reported first so callers can route mismatches to a
// distinct dead-letter reason.
func Verify(opp *domain.ExternalOpportunity, src *domain.Source, reg *Registry) VerifyResult {
	res := VerifyResult{OK: true}

	// 1. Source contract
	if !inList(src.Kinds, opp.Kind) {
		res.OK = false
		res.Mismatch = fmt.Sprintf("kind %q not declared by source (declared: %v)", opp.Kind, src.Kinds)
		return res
	}

	spec, ok := reg.Lookup(opp.Kind)
	if !ok {
		res.OK = false
		res.Mismatch = fmt.Sprintf("kind %q not in registry", opp.Kind)
		return res
	}

	// 2. Universal required
	for _, k := range spec.UniversalRequired {
		if !universalFieldPresent(opp, k) {
			res.OK = false
			res.Missing = append(res.Missing, k)
		}
	}

	// 3. Kind required
	for _, k := range spec.KindRequired {
		if !attrPresent(opp.Attributes, k) {
			res.OK = false
			res.Missing = append(res.Missing, k)
		}
	}

	// 4. Source override (tightens kind required)
	if extra, ok := src.RequiredAttributesByKind[opp.Kind]; ok {
		for _, k := range extra {
			if !attrPresent(opp.Attributes, k) {
				res.OK = false
				res.Missing = append(res.Missing, k)
			}
		}
	}

	// 5. Stray attributes (warning only; do not flip OK)
	known := map[string]struct{}{}
	for _, k := range spec.KindRequired {
		known[k] = struct{}{}
	}
	for _, k := range spec.KindOptional {
		known[k] = struct{}{}
	}
	for k := range opp.Attributes {
		if _, ok := known[k]; !ok {
			res.Extra = append(res.Extra, k)
		}
	}

	return res
}

func inList(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func universalFieldPresent(opp *domain.ExternalOpportunity, key string) bool {
	switch key {
	case "title":
		return opp.Title != ""
	case "description":
		return len(opp.Description) >= 50
	case "issuing_entity":
		return opp.IssuingEntity != ""
	case "apply_url":
		return opp.ApplyURL != ""
	case "anchor_country":
		return opp.AnchorLocation != nil && opp.AnchorLocation.Country != ""
	case "anchor_region":
		return opp.AnchorLocation != nil && opp.AnchorLocation.Region != ""
	case "anchor_city":
		return opp.AnchorLocation != nil && opp.AnchorLocation.City != ""
	}
	return false
}

func attrPresent(attrs map[string]any, key string) bool {
	if attrs == nil {
		return false
	}
	v, ok := attrs[key]
	if !ok {
		return false
	}
	switch x := v.(type) {
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	case []string:
		return len(x) > 0
	case nil:
		return false
	}
	return true
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./pkg/opportunity/...
```

Expected: 4 verify tests pass.

- [ ] **Step 5: Commit**

```bash
git add pkg/opportunity/verify.go pkg/opportunity/verify_test.go
git commit -m "feat(opportunity): Verify enforces source contract + spec required fields"
```

### Task 4.3: Wire Verify into pipeline; remove quality/gate

**Files:**
- Modify: `apps/crawler/service/<extract handler>.go` (or wherever extract → publish handoff lives)
- Modify: dead-letter publisher
- Delete: `pkg/quality/gate.go`
- Delete: `pkg/quality/gate_test.go`

- [ ] **Step 1: Locate the extract→publish call site**

```bash
grep -rn "quality\\.Check\\|gate\\.Check" --include='*.go' apps/ pkg/
```

This finds the call to the old quality gate. Replace with:

```go
res := opportunity.Verify(opp, src, registry)
if !res.OK {
	if err := publishRejected(ctx, opp, res); err != nil {
		util.Log(ctx).WithError(err).Warn("verify: failed to publish rejected variant")
	}
	telemetry.RecordVerifyRejection(opp.Kind, rejectionReason(res))
	return nil // do not propagate downstream
}
```

- [ ] **Step 2: Implement publishRejected**

Add to the same file (or a new `verify_publisher.go`):

```go
func publishRejected(ctx context.Context, opp *domain.ExternalOpportunity, res opportunity.VerifyResult) error {
	reasons := append([]string(nil), res.Missing...)
	if res.Mismatch != "" {
		reasons = append(reasons, res.Mismatch)
	}
	row := map[string]any{
		"variant_id":  opp.SourceID + "|" + opp.ExternalID,
		"source_id":   opp.SourceID,
		"kind":        opp.Kind,
		"title":       opp.Title,
		"reasons":     reasons,
		"rejected_at": time.Now().UTC(),
	}
	return rejectedWriter.Append(ctx, row) // appends to opportunities.variants_rejected via the writer's existing append-only path
}

func rejectionReason(r opportunity.VerifyResult) string {
	if r.Mismatch != "" {
		return "mismatch"
	}
	if len(r.Missing) > 0 {
		return "missing_" + r.Missing[0]
	}
	return "unknown"
}
```

- [ ] **Step 3: Delete quality/gate**

```bash
rm pkg/quality/gate.go pkg/quality/gate_test.go
# Check if pkg/quality has other files; if it's now empty, remove the directory.
ls pkg/quality/ 2>/dev/null && echo "(other files remain)" || rmdir pkg/quality/
```

Repair imports anywhere that still references `pkg/quality`:

```bash
grep -rn "pkg/quality" --include='*.go' | tee /tmp/quality-refs.txt
# Each ref needs to be removed/replaced.
```

- [ ] **Step 4: Add telemetry counter and rename JobsReady**

In `pkg/telemetry/metrics.go`:

```go
// Rename JobsReady → OpportunitiesReady with a kind attribute.
var OpportunitiesReady metric.Int64Counter
var VerifyRejections   metric.Int64Counter
var ExtractionLatency  metric.Float64Histogram

// in init / NewMeter:
OpportunitiesReady, err = meter.Int64Counter("pipeline.opportunities.ready",
	metric.WithDescription("Opportunities that reached ready stage; labelled by kind"),
)
VerifyRejections, err = meter.Int64Counter("pipeline.verify.rejections",
	metric.WithDescription("Variants rejected by Verify, labelled by kind and reason"),
)
ExtractionLatency, err = meter.Float64Histogram("pipeline.extraction.latency_seconds",
	metric.WithDescription("Extractor wall-clock latency; labelled by kind"),
)

func RecordOpportunityReady(kind string) {
	if OpportunitiesReady != nil {
		OpportunitiesReady.Add(context.Background(), 1,
			metric.WithAttributes(attribute.String("kind", kind)))
	}
}

func RecordVerifyRejection(kind, reason string) {
	if VerifyRejections != nil {
		VerifyRejections.Add(context.Background(), 1,
			metric.WithAttributes(attribute.String("kind", kind), attribute.String("reason", reason)))
	}
}

func RecordExtractionLatency(kind string, seconds float64) {
	if ExtractionLatency != nil {
		ExtractionLatency.Record(context.Background(), seconds,
			metric.WithAttributes(attribute.String("kind", kind)))
	}
}
```

Then find every existing `JobsReady.Add(...)` callsite and replace with `RecordOpportunityReady(opp.Kind)`:

```bash
grep -rn "JobsReady" --include='*.go'
```

For each match, swap to the new helper. Delete the old `JobsReady` symbol when no callers remain.

- [ ] **Step 5: Run tests**

```bash
go build ./...
go test ./...
```

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor(pipeline): replace quality/gate with opportunity.Verify; route rejections to variants_rejected"
```

### Phase 4 smoke check

```bash
go build ./...
go test ./...
go test -tags integration ./pkg/repository/... ./pkg/searchindex/...
```

Expected: green. Sources declare kinds; Verify rejects records that violate the contract; rejections land in their own Iceberg table for inspection.

---

## Phase 5 — Storage path & UI URL split

Outcome: R2 path becomes `<URLPrefix>/<slug>.json`. KV rebuild iterates kinds. Hugo content split per kind. Per-kind sitemap shards.

### Task 5.1: R2 path uses URLPrefix

**Files:**
- Modify: `pkg/publish/r2.go`
- Test: `pkg/publish/r2_test.go`

- [ ] **Step 1: Add a failing test**

```go
// pkg/publish/r2_test.go — append
func TestObjectKey_ByKind(t *testing.T) {
	cases := []struct{ kind, prefix, slug, want string }{
		{"job", "jobs", "go-eng-acme-abc", "jobs/go-eng-acme-abc.json"},
		{"scholarship", "scholarships", "msc-eth-xyz", "scholarships/msc-eth-xyz.json"},
		{"tender", "tenders", "rfp-foo", "tenders/rfp-foo.json"},
	}
	for _, c := range cases {
		if got := ObjectKey(c.prefix, c.slug); got != c.want {
			t.Errorf("ObjectKey(%q,%q) = %q, want %q", c.prefix, c.slug, got, c.want)
		}
	}
}

func TestTranslationKey_ByKind(t *testing.T) {
	got := TranslationKey("scholarships", "msc-eth-xyz", "fr")
	want := "scholarships/msc-eth-xyz/fr.json"
	if got != want {
		t.Errorf("TranslationKey = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/publish/...`
Expected: build fails (current `ObjectKey` may take different args, or be hardcoded).

- [ ] **Step 3: Implement**

In `pkg/publish/r2.go`:

```go
// ObjectKey returns the R2 object key for an opportunity body file.
// The prefix is Spec.URLPrefix (jobs, scholarships, tenders, ...).
func ObjectKey(prefix, slug string) string {
	return prefix + "/" + slug + ".json"
}

// TranslationKey returns the R2 object key for a translated body file.
func TranslationKey(prefix, slug, lang string) string {
	return prefix + "/" + slug + "/" + lang + ".json"
}
```

Update callers of the old hardcoded `"jobs/" + slug + ".json"`:

```bash
grep -rn '"jobs/"' --include='*.go' pkg/ apps/ | tee /tmp/jobs-prefix-callers.txt
```

For each caller, change to `ObjectKey(spec.URLPrefix, slug)` where `spec` comes from `registry.Resolve(opp.Kind)`.

- [ ] **Step 4: Run tests**

```bash
go test ./pkg/publish/...
go build ./...
```

- [ ] **Step 5: Commit**

```bash
git add pkg/publish/ apps/
git commit -m "refactor(publish): R2 path uses Spec.URLPrefix, not hardcoded jobs/"
```

### Task 5.2: KV rebuild iterates kinds

**Files:**
- Modify: `apps/worker/service/kv_rebuild.go`

- [ ] **Step 1: Update rebuild to iterate registry kinds**

Locate the existing R2 list call (currently `Prefix: aws.String("jobs/")`):

```go
// Replace single-prefix walk with per-kind walk
for _, kind := range r.registry.Known() {
	spec := r.registry.Resolve(kind)
	if err := r.walkPrefix(ctx, spec.URLPrefix+"/"); err != nil {
		return fmt.Errorf("kv-rebuild: kind %q: %w", kind, err)
	}
}
```

`walkPrefix` is the extracted helper that contains the existing per-page loop logic.

- [ ] **Step 2: Test**

Run the existing kv_rebuild test (or its integration test if present):

```bash
go test -tags integration ./apps/worker/...
```

- [ ] **Step 3: Commit**

```bash
git add apps/worker/service/kv_rebuild.go
git commit -m "refactor(worker): KV rebuild iterates registry.Known() prefixes"
```

### Task 5.3: Hugo content split per kind

**Files:**
- Move: `ui/content/jobs/_index.md` (keep)
- Create: `ui/content/scholarships/_index.md`
- Create: `ui/content/tenders/_index.md`
- Create: `ui/content/deals/_index.md`
- Create: `ui/content/funding/_index.md`
- Create: `ui/layouts/<prefix>/list.html` per kind (or shared template via Hugo lookup)
- Create: `ui/layouts/<prefix>/single.html` per kind
- Create: `ui/layouts/partials/opportunity-card.html` (renamed from job-card.html)

- [ ] **Step 1: Create per-kind index pages**

```bash
mkdir -p ui/content/{scholarships,tenders,deals,funding}
```

For each new directory, author `_index.md`:

```markdown
---
title: "Scholarships"
description: "Browse scholarship opportunities."
---
```

(Replace title/description per kind.)

- [ ] **Step 2: Move templates and rename partial**

```bash
git mv ui/layouts/partials/job-card.html ui/layouts/partials/opportunity-card.html
```

In `ui/layouts/partials/opportunity-card.html`, replace the existing job-only badges block with kind-conditional rendering:

```html
{{ $kind := .Params.kind | default "job" }}
{{ with .Params.issuing_entity }}<span class="text-sm text-gray-700">{{ . }}</span>{{ end }}

{{ if eq $kind "job" }}
  {{ with .Params.employment_type }}<span class="badge-type">{{ . }}</span>{{ end }}
  {{ if and .Params.amount_min (gt .Params.amount_min 0.0) }}
    <span class="text-sm font-medium text-green-700">
      {{ .Params.currency }} {{ lang.FormatNumber 0 .Params.amount_min }}–{{ lang.FormatNumber 0 .Params.amount_max }}
    </span>
  {{ end }}
{{ else if eq $kind "scholarship" }}
  {{ with .Params.degree_level }}<span class="badge-type">{{ . }}</span>{{ end }}
  {{ with .Params.field_of_study }}<span class="badge-type">{{ . }}</span>{{ end }}
  {{ with .Params.deadline }}<span class="text-sm text-orange-700">Apply by {{ dateFormat "Jan 2, 2006" . }}</span>{{ end }}
{{ else if eq $kind "tender" }}
  {{ with .Params.procurement_domain }}<span class="badge-type">{{ . }}</span>{{ end }}
  {{ with .Params.deadline }}<span class="text-sm text-orange-700">Closes {{ dateFormat "Jan 2, 2006" . }}</span>{{ end }}
{{ else if eq $kind "deal" }}
  {{ with .Params.discount_percent }}<span class="badge-type">{{ . }}% off</span>{{ end }}
  {{ with .Params.expiry }}<span class="text-sm text-orange-700">Expires {{ dateFormat "Jan 2, 2006" . }}</span>{{ end }}
{{ else if eq $kind "funding" }}
  {{ with .Params.focus_area }}<span class="badge-type">{{ . }}</span>{{ end }}
  {{ with .Params.deadline }}<span class="text-sm text-orange-700">Apply by {{ dateFormat "Jan 2, 2006" . }}</span>{{ end }}
{{ end }}
```

Update every reference to `partials/job-card.html` in other Hugo templates:

```bash
grep -rln "job-card.html" ui/layouts/ | xargs sed -i 's/job-card.html/opportunity-card.html/g'
```

- [ ] **Step 3: Per-kind list/single layouts via Hugo lookup**

Hugo's section-based template lookup means `ui/layouts/<section>/list.html` and `ui/layouts/<section>/single.html` are picked up automatically when content lives under `ui/content/<section>/`. Copy the existing job templates as starting points for each new kind:

```bash
for k in scholarships tenders deals funding; do
  mkdir -p ui/layouts/$k
  cp ui/layouts/jobs/list.html ui/layouts/$k/list.html
  cp ui/layouts/jobs/single.html ui/layouts/$k/single.html
done
```

In each copied template, customise headings and any kind-specific copy.

- [ ] **Step 4: Per-kind sitemap shards**

In `ui/config.toml` (or `ui/hugo.toml`), the existing `[sitemap]` section is global. To shard by kind, add a custom output template `ui/layouts/_default/sitemap.xml` that filters by `.Section`. Or simpler: leave the global sitemap and let Hugo include all sections; revisit shard splitting as a follow-up if SEO needs it.

For now: confirm that `hugo --baseURL https://opportunities.stawi.org` produces a single sitemap that includes all five sections. Defer multi-shard split.

- [ ] **Step 5: Hugo build smoke**

```bash
cd ui
hugo --minify --baseURL https://opportunities.stawi.org
ls public/jobs public/scholarships public/tenders public/deals public/funding
```

Expected: each per-kind directory exists with an index.html.

- [ ] **Step 6: Commit**

```bash
cd ..
git add ui/
git commit -m "refactor(ui): per-kind Hugo content + opportunity-card partial"
```

### Phase 5 smoke check

```bash
go build ./...
go test ./...
cd ui && hugo --minify && cd ..
```

Expected: green Go build, Hugo produces five per-kind directories under `public/`.

---

## Phase 6 — Extraction dispatch

Outcome: LLM extractor splits universal prefix + kind-specific body. Single-kind sources skip the classifier; multi-kind sources classify before extracting.

### Task 6.1: Universal prefix + kind body

**Files:**
- Modify: `pkg/extraction/extractor.go`
- Test: `pkg/extraction/extractor_test.go`

- [ ] **Step 1: Define the universal prefix**

In `pkg/extraction/extractor.go`, add:

```go
const universalPromptPrefix = `You are an extraction assistant. Read the provided HTML/Markdown
and produce a single JSON object that strictly matches the schema below.
Output ONLY the JSON object — no prose, no code fences. Empty/unknown
fields should be omitted (not "" or null) unless the schema explicitly
requires them. Use ISO 3166-1 alpha-2 for country codes and ISO 4217 for
currency codes. Dates are RFC3339 timestamps in UTC.`
```

- [ ] **Step 2: Wire kind-specific prompt selection**

Replace the existing `systemPrompt` constant usage with:

```go
func (e *Extractor) buildPrompt(kind string) string {
	spec := e.registry.Resolve(kind)
	return universalPromptPrefix + "\n\nSchema for kind=" + kind + ":\n" + spec.ExtractionPrompt
}
```

The Extractor struct gains `registry *opportunity.Registry`, populated at construction.

Update `Extractor.Extract` to:

```go
func (e *Extractor) Extract(ctx context.Context, html string, sourceKinds []string) (*domain.ExternalOpportunity, error) {
	kind, err := e.pickKind(ctx, html, sourceKinds)
	if err != nil {
		return nil, err
	}
	prompt := e.buildPrompt(kind)
	raw, err := e.llm.Complete(ctx, prompt+"\n\nDocument:\n"+html)
	if err != nil {
		return nil, err
	}
	opp, err := parseExtractionJSON(raw, kind)
	if err != nil {
		return nil, err
	}
	opp.Kind = kind
	return opp, nil
}

func (e *Extractor) pickKind(ctx context.Context, html string, sourceKinds []string) (string, error) {
	if len(sourceKinds) == 1 {
		return sourceKinds[0], nil
	}
	if len(sourceKinds) == 0 {
		// Source declared no kinds; classifier picks from all known.
		sourceKinds = e.registry.Known()
	}
	classifierPrompt := fmt.Sprintf(`Classify the document as one of: %s.
Output ONLY the classification string.`, strings.Join(sourceKinds, ", "))
	out, err := e.llm.Complete(ctx, classifierPrompt+"\n\n"+html)
	if err != nil {
		return "", err
	}
	pick := strings.TrimSpace(strings.ToLower(out))
	for _, k := range sourceKinds {
		if pick == k {
			return k, nil
		}
	}
	return "", fmt.Errorf("classifier returned unknown kind %q (allowed: %v)", pick, sourceKinds)
}

func parseExtractionJSON(raw, kind string) (*domain.ExternalOpportunity, error) {
	// Decode into a map, then split universal fields from kind-specific
	// attributes.
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, fmt.Errorf("parse extraction JSON: %w", err)
	}
	opp := &domain.ExternalOpportunity{Kind: kind, Attributes: map[string]any{}}
	universal := map[string]struct{}{
		"title": {}, "description": {}, "issuing_entity": {}, "apply_url": {},
		"anchor_country": {}, "anchor_region": {}, "anchor_city": {},
		"remote": {}, "geo_scope": {}, "currency": {}, "amount_min": {}, "amount_max": {},
		"deadline": {}, "categories": {}, "lat": {}, "lon": {},
	}
	for k, v := range m {
		if _, ok := universal[k]; !ok {
			opp.Attributes[k] = v
			continue
		}
		assignUniversal(opp, k, v)
	}
	return opp, nil
}

func assignUniversal(opp *domain.ExternalOpportunity, k string, v any) {
	switch k {
	case "title":
		opp.Title, _ = v.(string)
	case "description":
		opp.Description, _ = v.(string)
	case "issuing_entity":
		opp.IssuingEntity, _ = v.(string)
	case "apply_url":
		opp.ApplyURL, _ = v.(string)
	case "remote":
		if b, ok := v.(bool); ok {
			opp.Remote = b
		}
	case "geo_scope":
		opp.GeoScope, _ = v.(string)
	case "currency":
		opp.Currency, _ = v.(string)
	case "amount_min":
		opp.AmountMin, _ = toFloat(v)
	case "amount_max":
		opp.AmountMax, _ = toFloat(v)
	case "deadline":
		if s, ok := v.(string); ok {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				opp.Deadline = &t
			}
		}
	case "categories":
		if a, ok := v.([]any); ok {
			for _, x := range a {
				if s, ok := x.(string); ok {
					opp.Categories = append(opp.Categories, s)
				}
			}
		}
	case "anchor_country":
		ensureLoc(opp).Country, _ = v.(string)
	case "anchor_region":
		ensureLoc(opp).Region, _ = v.(string)
	case "anchor_city":
		ensureLoc(opp).City, _ = v.(string)
	case "lat":
		ensureLoc(opp).Lat, _ = toFloat(v)
	case "lon":
		ensureLoc(opp).Lon, _ = toFloat(v)
	}
}

func ensureLoc(opp *domain.ExternalOpportunity) *domain.Location {
	if opp.AnchorLocation == nil {
		opp.AnchorLocation = &domain.Location{}
	}
	return opp.AnchorLocation
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	}
	return 0, false
}
```

(Imports: `encoding/json`, `fmt`, `strconv`, `strings`, `time`.)

- [ ] **Step 3: Tests cover both single-kind and multi-kind paths**

Add to `pkg/extraction/extractor_test.go`:

```go
func TestExtract_SingleKindSkipsClassifier(t *testing.T) {
	llm := &fakeLLM{
		extract: `{"title":"Senior Go Engineer","description":"...long enough body...","issuing_entity":"Acme","apply_url":"https://acme.example/apply","anchor_country":"KE","employment_type":"full-time"}`,
	}
	reg, _ := opportunity.LoadFromDir("../../definitions/opportunity-kinds")
	e := &Extractor{llm: llm, registry: reg}
	opp, err := e.Extract(context.Background(), "<html/>", []string{"job"})
	if err != nil {
		t.Fatal(err)
	}
	if opp.Kind != "job" {
		t.Errorf("Kind=%q, want job", opp.Kind)
	}
	if llm.classifyCalls != 0 {
		t.Errorf("classifier called %d times for single-kind source", llm.classifyCalls)
	}
	if opp.Attributes["employment_type"] != "full-time" {
		t.Errorf("attribute lost: %+v", opp.Attributes)
	}
}

func TestExtract_MultiKindClassifies(t *testing.T) {
	llm := &fakeLLM{
		classify: "scholarship",
		extract:  `{"title":"MSc","description":"long enough body for verify","issuing_entity":"ETH","field_of_study":"Climate","deadline":"2026-12-01T00:00:00Z"}`,
	}
	reg, _ := opportunity.LoadFromDir("../../definitions/opportunity-kinds")
	e := &Extractor{llm: llm, registry: reg}
	opp, err := e.Extract(context.Background(), "<html/>", []string{"job", "scholarship"})
	if err != nil {
		t.Fatal(err)
	}
	if opp.Kind != "scholarship" {
		t.Errorf("Kind=%q, want scholarship", opp.Kind)
	}
	if opp.Attributes["field_of_study"] != "Climate" {
		t.Errorf("attribute lost: %+v", opp.Attributes)
	}
}
```

`fakeLLM` is a minimal stub:

```go
type fakeLLM struct {
	classify, extract string
	classifyCalls     int
}

func (f *fakeLLM) Complete(ctx context.Context, prompt string) (string, error) {
	if strings.Contains(prompt, "Classify the document") {
		f.classifyCalls++
		return f.classify, nil
	}
	return f.extract, nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./pkg/extraction/...
```

- [ ] **Step 5: Wire registry into Extractor construction sites**

```bash
grep -rn "extraction\\.New" --include='*.go'
```

Each construction site gains a `registry *opportunity.Registry` argument; pass through from app boot.

- [ ] **Step 6: Commit**

```bash
git add pkg/extraction/ apps/
git commit -m "feat(extraction): two-stage extractor (classify → kind-specific prompt)"
```

### Task 6.2: Bundled gazetteer geocoder

**Files:**
- Create: `pkg/geocode/geocoder.go`
- Create: `pkg/geocode/cities.tsv` (top ~10k cities, tab-separated)
- Test: `pkg/geocode/geocoder_test.go`

The geocoder runs in normalize after extraction so `Lat/Lon` get populated when the LLM emits a recognisable city name. Misses leave coords zero — radius search just skips those records.

- [ ] **Step 1: Bundle the city dataset**

Pick a permissively-licensed dataset (e.g. GeoNames `cities5000.zip` is CC BY 4.0). Reduce to columns: `name\tcountry\tregion\tlat\tlon\tpopulation`. Sort by population descending and keep the top 10000 rows. Save as `pkg/geocode/cities.tsv` (a few hundred KB; small enough to bundle).

- [ ] **Step 2: Write the failing test**

```go
// pkg/geocode/geocoder_test.go
package geocode

import "testing"

func TestGeocode_KnownCity(t *testing.T) {
	g := New()
	loc, ok := g.Lookup("Nairobi", "KE")
	if !ok {
		t.Fatal("expected Nairobi/KE to resolve")
	}
	if loc.Lat < -2 || loc.Lat > 0 || loc.Lon < 36 || loc.Lon > 38 {
		t.Errorf("Nairobi coords look wrong: %+v", loc)
	}
}

func TestGeocode_Miss(t *testing.T) {
	g := New()
	if _, ok := g.Lookup("Atlantis", ""); ok {
		t.Fatal("Atlantis should not resolve")
	}
}

func TestGeocode_CaseInsensitive(t *testing.T) {
	g := New()
	if _, ok := g.Lookup("nAirObI", "KE"); !ok {
		t.Fatal("expected case-insensitive match")
	}
}
```

- [ ] **Step 3: Implement**

```go
// pkg/geocode/geocoder.go
package geocode

import (
	"bufio"
	_ "embed"
	"strconv"
	"strings"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

//go:embed cities.tsv
var citiesTSV string

type Geocoder struct {
	byKey map[string]domain.Location // key = lower(name) + "|" + upper(country)
	byCity map[string]domain.Location // key = lower(name) — fallback when country empty
}

// New parses the embedded cities.tsv at construction. Cheap (~10k rows).
func New() *Geocoder {
	g := &Geocoder{byKey: map[string]domain.Location{}, byCity: map[string]domain.Location{}}
	scanner := bufio.NewScanner(strings.NewReader(citiesTSV))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		parts := strings.Split(scanner.Text(), "\t")
		if len(parts) < 5 {
			continue
		}
		name, country, region := parts[0], strings.ToUpper(parts[1]), parts[2]
		lat, _ := strconv.ParseFloat(parts[3], 64)
		lon, _ := strconv.ParseFloat(parts[4], 64)
		loc := domain.Location{Country: country, Region: region, City: name, Lat: lat, Lon: lon}
		g.byKey[strings.ToLower(name)+"|"+country] = loc
		// First-write-wins for the country-less fallback so the highest-population city wins.
		key := strings.ToLower(name)
		if _, exists := g.byCity[key]; !exists {
			g.byCity[key] = loc
		}
	}
	return g
}

// Lookup returns a Location for the given city + country. country is optional;
// if blank, we return the highest-population city with that name globally.
func (g *Geocoder) Lookup(city, country string) (domain.Location, bool) {
	city = strings.TrimSpace(city)
	if city == "" {
		return domain.Location{}, false
	}
	if country != "" {
		if loc, ok := g.byKey[strings.ToLower(city)+"|"+strings.ToUpper(country)]; ok {
			return loc, true
		}
		return domain.Location{}, false
	}
	loc, ok := g.byCity[strings.ToLower(city)]
	return loc, ok
}

// Enrich populates AnchorLocation.Lat/Lon (and Region if missing) on opp
// when the gazetteer has a hit. No-op when extraction already supplied
// coords or when the city/country combination is unknown.
func (g *Geocoder) Enrich(opp *domain.ExternalOpportunity) {
	if opp.AnchorLocation == nil || opp.AnchorLocation.City == "" {
		return
	}
	if opp.AnchorLocation.HasCoords() {
		return
	}
	loc, ok := g.Lookup(opp.AnchorLocation.City, opp.AnchorLocation.Country)
	if !ok {
		return
	}
	opp.AnchorLocation.Lat = loc.Lat
	opp.AnchorLocation.Lon = loc.Lon
	if opp.AnchorLocation.Region == "" {
		opp.AnchorLocation.Region = loc.Region
	}
}
```

- [ ] **Step 4: Wire into normalize**

In `pkg/normalize/normalize.go`, after the existing field-cleanup logic and before returning, add:

```go
if n.geocoder != nil {
	n.geocoder.Enrich(opp)
}
```

The `Normalizer` struct gains a `geocoder *geocode.Geocoder` field, populated at construction. Construction sites pass `geocode.New()` once and reuse.

- [ ] **Step 5: Run tests**

```bash
go test ./pkg/geocode/...
go test ./pkg/normalize/...
```

- [ ] **Step 6: Commit**

```bash
git add pkg/geocode/ pkg/normalize/ apps/
git commit -m "feat(geocode): bundled-gazetteer Geocoder; wired into normalize"
```

### Phase 6 smoke check

```bash
go build ./...
go test ./...
```

Expected: green. The extractor now dispatches per kind; geocoder enriches Lat/Lon for known cities. Production behaviour for existing job sources is unchanged because they declare exactly one kind (`["job"]`), skipping the classifier.

---

## Phase 7 — Matchers + per-kind onboarding

Outcome: `apps/candidates/` renamed `apps/matching/`, hosting all five kind matchers via a `Matcher` interface. Preferences event becomes polymorphic. UI gains five onboarding flows + dashboard tabs per opted-in kind.

### Task 7.1: Rename apps/candidates → apps/matching

**Files:**
- Move: `apps/candidates/*` → `apps/matching/*`

- [ ] **Step 1: Move tree**

```bash
git mv apps/candidates apps/matching
grep -rln "apps/candidates" --include='*.go' --include='*.yaml' --include='*.toml' --include='*.md' --include='Dockerfile*' --include='Makefile' | xargs sed -i 's|apps/candidates|apps/matching|g'
grep -rln "candidates-" --include='*.yaml' definitions/ | xargs sed -i 's|opportunities-candidates|opportunities-matching|g'
```

- [ ] **Step 2: Update Helm chart name (deployments repo)**

In `/home/j/code/antinvestor/deployments/`:

```bash
git mv manifests/namespaces/opportunities/helmrelease-candidates.yaml manifests/namespaces/opportunities/helmrelease-matching.yaml
```

Update the resource names + image references inside.

- [ ] **Step 3: Build + test**

```bash
go build ./...
go test ./...
```

- [ ] **Step 4: Commit (both repos)**

```bash
# opportunities repo
git add -A
git commit -m "refactor: apps/candidates → apps/matching"

# deployments repo
cd /home/j/code/antinvestor/deployments
git add -A
git commit -m "refactor: candidates → matching app rename"
cd -
```

### Task 7.2: Matcher interface + registry

**Files:**
- Create: `apps/matching/service/matchers/matcher.go`
- Create: `apps/matching/service/matchers/matcher_test.go`

- [ ] **Step 1: Write the failing test**

```go
// apps/matching/service/matchers/matcher_test.go
package matchers

import "testing"

func TestRegistry_Register_Lookup(t *testing.T) {
	r := NewRegistry()
	stub := &stubMatcher{kind: "job"}
	r.Register(stub)
	got, ok := r.For("job")
	if !ok || got != stub {
		t.Fatalf("For(job) = %v,%v, want stub,true", got, ok)
	}
	if _, ok := r.For("unknown"); ok {
		t.Fatal("For(unknown) should report missing")
	}
}

type stubMatcher struct{ kind string }

func (s *stubMatcher) Kind() string { return s.kind }
func (s *stubMatcher) SearchFilter(_ []byte) (any, error) { return nil, nil }
func (s *stubMatcher) Score(_ context.Context, _ []byte, _ any) (float64, error) { return 0, nil }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./apps/matching/service/matchers/...`
Expected: build fails.

- [ ] **Step 3: Implement**

```go
// apps/matching/service/matchers/matcher.go
package matchers

import (
	"context"
	"encoding/json"
)

// Matcher is the contract every kind-specific matcher implements.
// Registered with the matchers Registry at app boot; the router selects
// by Kind().
type Matcher interface {
	Kind() string
	// SearchFilter returns a kind-scoped Manticore filter expression
	// (or equivalent) built from the candidate's preferences blob.
	SearchFilter(prefs json.RawMessage) (any, error)
	// Score ranks one opportunity against the same preferences blob.
	// Returns a value in [0, 1].
	Score(ctx context.Context, prefs json.RawMessage, opp any) (float64, error)
}

type Registry struct{ m map[string]Matcher }

func NewRegistry() *Registry { return &Registry{m: map[string]Matcher{}} }

func (r *Registry) Register(mt Matcher) { r.m[mt.Kind()] = mt }

func (r *Registry) For(kind string) (Matcher, bool) {
	mt, ok := r.m[kind]
	return mt, ok
}

func (r *Registry) Kinds() []string {
	out := make([]string, 0, len(r.m))
	for k := range r.m {
		out = append(out, k)
	}
	return out
}
```

(The `any` types on SearchFilter/Score are temporary scaffolding; later tasks tighten them to concrete `searchindex.Filter` and `*domain.Opportunity` types once those types exist in the matcher package's dependency tree.)

- [ ] **Step 4: Run test**

```bash
go test ./apps/matching/service/matchers/...
```

- [ ] **Step 5: Commit**

```bash
git add apps/matching/service/matchers/
git commit -m "feat(matching): Matcher interface + Registry"
```

### Task 7.3: Job matcher (port existing logic)

**Files:**
- Create: `apps/matching/service/matchers/job/matcher.go`
- Create: `apps/matching/service/matchers/job/prefs.go`
- Test: `apps/matching/service/matchers/job/matcher_test.go`

- [ ] **Step 1: Define preferences shape**

```go
// apps/matching/service/matchers/job/prefs.go
package job

import "github.com/stawi-opportunities/opportunities/pkg/matching/locationpref"

// JobPreferences is the per-candidate preferences blob captured by the
// job onboarding flow. Stored as JSON inside PreferencesUpdatedV1.OptIns["job"].
type JobPreferences struct {
	TargetRoles     []string                       `json:"target_roles"`
	EmploymentTypes []string                       `json:"employment_types"`
	SeniorityLevels []string                       `json:"seniority_levels"`
	SalaryMin       float64                        `json:"salary_min"`
	Currency        string                         `json:"currency"`
	Locations       locationpref.LocationPreference `json:"locations"`
}
```

(Create `pkg/matching/locationpref/locationpref.go` with the `LocationPreference` struct from the spec, used by every kind's prefs.)

- [ ] **Step 2: Port the existing match logic**

```go
// apps/matching/service/matchers/job/matcher.go
package job

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/stawi-opportunities/opportunities/apps/matching/service/matchers"
	"github.com/stawi-opportunities/opportunities/pkg/searchindex"
)

type Matcher struct{}

func New() *Matcher { return &Matcher{} }

func (m *Matcher) Kind() string { return "job" }

func (m *Matcher) SearchFilter(prefs json.RawMessage) (searchindex.Filter, error) {
	var p JobPreferences
	if err := json.Unmarshal(prefs, &p); err != nil {
		return searchindex.Filter{}, err
	}
	f := searchindex.Filter{Kind: "job"}
	if len(p.EmploymentTypes) > 0 {
		f.AnyOf = append(f.AnyOf, searchindex.AnyOf{Field: "employment_type", Values: p.EmploymentTypes})
	}
	if len(p.SeniorityLevels) > 0 {
		f.AnyOf = append(f.AnyOf, searchindex.AnyOf{Field: "seniority", Values: p.SeniorityLevels})
	}
	if p.SalaryMin > 0 {
		f.RangeMin = append(f.RangeMin, searchindex.RangeMin{Field: "amount_min", Value: p.SalaryMin})
	}
	if len(p.Locations.Countries) > 0 {
		f.AnyOf = append(f.AnyOf, searchindex.AnyOf{Field: "country", Values: p.Locations.Countries})
	}
	if p.Locations.RemoteOK {
		f.OrTerm = "remote = 1"
	}
	if p.Locations.NearLat != 0 && p.Locations.RadiusKm > 0 {
		f.GeoDist = &searchindex.GeoDist{Lat: p.Locations.NearLat, Lon: p.Locations.NearLon, RadiusKm: p.Locations.RadiusKm}
	}
	return f, nil
}

func (m *Matcher) Score(ctx context.Context, prefs json.RawMessage, opp any) (float64, error) {
	var p JobPreferences
	if err := json.Unmarshal(prefs, &p); err != nil {
		return 0, err
	}
	o, _ := opp.(map[string]any)
	score := 0.0
	if title, _ := o["title"].(string); title != "" {
		for _, role := range p.TargetRoles {
			if strings.Contains(strings.ToLower(title), strings.ToLower(role)) {
				score += 0.4
				break
			}
		}
	}
	if amt, _ := o["amount_min"].(float64); amt >= p.SalaryMin {
		score += 0.3
	}
	if amt, _ := o["amount_min"].(float64); amt >= 1.5*p.SalaryMin {
		score += 0.2
	}
	if score > 1.0 {
		score = 1.0
	}
	return score, nil
}

var _ matchers.Matcher = (*Matcher)(nil)
```

- [ ] **Step 3: Add filter helpers in pkg/searchindex**

```go
// pkg/searchindex/filter.go
package searchindex

type Filter struct {
	Kind     string
	AnyOf    []AnyOf
	RangeMin []RangeMin
	GeoDist  *GeoDist
	OrTerm   string
}

type AnyOf struct {
	Field  string
	Values []string
}

type RangeMin struct {
	Field string
	Value float64
}

type GeoDist struct {
	Lat, Lon float64
	RadiusKm int
}

// SQL renders the filter as a Manticore WHERE clause.
func (f Filter) SQL() string {
	parts := []string{}
	if f.Kind != "" {
		parts = append(parts, "kind = '"+f.Kind+"'")
	}
	for _, a := range f.AnyOf {
		quoted := make([]string, len(a.Values))
		for i, v := range a.Values {
			quoted[i] = "'" + strings.ReplaceAll(v, "'", "''") + "'"
		}
		parts = append(parts, a.Field+" IN ("+strings.Join(quoted, ",")+")")
	}
	for _, r := range f.RangeMin {
		parts = append(parts, fmt.Sprintf("%s >= %g", r.Field, r.Value))
	}
	if f.GeoDist != nil {
		parts = append(parts, fmt.Sprintf("GEODIST(lat, lon, %g, %g, {in=deg, out=km}) <= %d",
			f.GeoDist.Lat, f.GeoDist.Lon, f.GeoDist.RadiusKm))
	}
	clause := strings.Join(parts, " AND ")
	if f.OrTerm != "" {
		clause = "(" + clause + ") OR " + f.OrTerm
	}
	return clause
}
```

- [ ] **Step 4: Test**

```go
// apps/matching/service/matchers/job/matcher_test.go
package job

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSearchFilter_BuildsKindClause(t *testing.T) {
	prefs := json.RawMessage(`{"employment_types":["full-time"],"salary_min":80000}`)
	f, err := New().SearchFilter(prefs)
	if err != nil {
		t.Fatal(err)
	}
	sql := f.SQL()
	if !strings.Contains(sql, "kind = 'job'") {
		t.Errorf("missing kind clause: %s", sql)
	}
	if !strings.Contains(sql, "employment_type IN ('full-time')") {
		t.Errorf("missing employment_type clause: %s", sql)
	}
	if !strings.Contains(sql, "amount_min >= 80000") {
		t.Errorf("missing salary clause: %s", sql)
	}
}

func TestScore_TitleAndSalaryContribute(t *testing.T) {
	prefs := json.RawMessage(`{"target_roles":["Go Engineer"],"salary_min":80000}`)
	opp := map[string]any{"title": "Senior Go Engineer", "amount_min": 130000.0}
	got, err := New().Score(context.Background(), prefs, opp)
	if err != nil || got < 0.8 {
		t.Fatalf("Score=%v err=%v, want >= 0.8", got, err)
	}
}
```

```bash
go test ./apps/matching/service/matchers/job/...
```

- [ ] **Step 5: Commit**

```bash
git add apps/matching/service/matchers/job/ pkg/searchindex/filter.go pkg/matching/locationpref/
git commit -m "feat(matching): job matcher (port + Matcher interface)"
```

### Task 7.4: Scholarship matcher

**Files:**
- Create: `apps/matching/service/matchers/scholarship/matcher.go`
- Create: `apps/matching/service/matchers/scholarship/prefs.go`
- Test: `apps/matching/service/matchers/scholarship/matcher_test.go`

- [ ] **Step 1: Define prefs**

```go
// apps/matching/service/matchers/scholarship/prefs.go
package scholarship

import (
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/matching/locationpref"
)

type ScholarshipPreferences struct {
	DegreeLevels   []string                       `json:"degree_levels"`
	FieldsOfStudy  []string                       `json:"fields_of_study"`
	Nationality    string                         `json:"nationality"`
	GPAMin         float64                        `json:"gpa_min"`
	Locations      locationpref.LocationPreference `json:"locations"`
	DeadlineWithin time.Duration                  `json:"deadline_within"`
}
```

- [ ] **Step 2: Implement matcher**

```go
// apps/matching/service/matchers/scholarship/matcher.go
package scholarship

import (
	"context"
	"encoding/json"
	"time"

	"github.com/stawi-opportunities/opportunities/apps/matching/service/matchers"
	"github.com/stawi-opportunities/opportunities/pkg/searchindex"
)

type Matcher struct{}

func New() *Matcher { return &Matcher{} }
func (m *Matcher) Kind() string { return "scholarship" }

func (m *Matcher) SearchFilter(prefs json.RawMessage) (searchindex.Filter, error) {
	var p ScholarshipPreferences
	if err := json.Unmarshal(prefs, &p); err != nil {
		return searchindex.Filter{}, err
	}
	f := searchindex.Filter{Kind: "scholarship"}
	if len(p.DegreeLevels) > 0 {
		f.AnyOf = append(f.AnyOf, searchindex.AnyOf{Field: "degree_level", Values: p.DegreeLevels})
	}
	if len(p.FieldsOfStudy) > 0 {
		f.AnyOf = append(f.AnyOf, searchindex.AnyOf{Field: "field_of_study", Values: p.FieldsOfStudy})
	}
	if len(p.Locations.Countries) > 0 {
		f.AnyOf = append(f.AnyOf, searchindex.AnyOf{Field: "country", Values: p.Locations.Countries})
	}
	return f, nil
}

func (m *Matcher) Score(ctx context.Context, prefs json.RawMessage, opp any) (float64, error) {
	var p ScholarshipPreferences
	if err := json.Unmarshal(prefs, &p); err != nil {
		return 0, err
	}
	o, _ := opp.(map[string]any)
	score := 0.0
	if dl, _ := o["degree_level"].(string); dl != "" {
		for _, want := range p.DegreeLevels {
			if dl == want {
				score += 0.4
				break
			}
		}
	}
	if fos, _ := o["field_of_study"].(string); fos != "" {
		for _, want := range p.FieldsOfStudy {
			if fos == want {
				score += 0.4
				break
			}
		}
	}
	// Closer deadlines penalised slightly to favour planning ahead.
	if dl, _ := o["deadline"].(string); dl != "" {
		if t, err := time.Parse(time.RFC3339, dl); err == nil {
			if time.Until(t) > 30*24*time.Hour {
				score += 0.2
			}
		}
	}
	if score > 1.0 {
		score = 1.0
	}
	return score, nil
}

var _ matchers.Matcher = (*Matcher)(nil)
```

- [ ] **Step 3: Test**

Mirror the job matcher test with scholarship-shaped fixtures. Verify `kind = 'scholarship'` appears in the filter SQL.

- [ ] **Step 4: Commit**

```bash
git add apps/matching/service/matchers/scholarship/
git commit -m "feat(matching): scholarship matcher"
```

### Task 7.5: Stub matchers (tender, deal, funding)

**Files:**
- Create: `apps/matching/service/matchers/{tender,deal,funding}/matcher.go` (each with `prefs.go`)

- [ ] **Step 1: Each stub matcher**

For each of `tender`, `deal`, `funding`, create a matcher that returns a kind-only filter and a constant 0.5 score. The stub satisfies the `Matcher` interface so the router doesn't panic when a candidate opts into one of these kinds, but the match quality is uniform until we ship a real scorer.

Example for tender:

```go
// apps/matching/service/matchers/tender/matcher.go
package tender

import (
	"context"
	"encoding/json"

	"github.com/stawi-opportunities/opportunities/apps/matching/service/matchers"
	"github.com/stawi-opportunities/opportunities/pkg/searchindex"
)

type Matcher struct{}

func New() *Matcher        { return &Matcher{} }
func (*Matcher) Kind() string { return "tender" }

func (*Matcher) SearchFilter(prefs json.RawMessage) (searchindex.Filter, error) {
	return searchindex.Filter{Kind: "tender"}, nil
}

func (*Matcher) Score(ctx context.Context, prefs json.RawMessage, opp any) (float64, error) {
	return 0.5, nil
}

var _ matchers.Matcher = (*Matcher)(nil)
```

Same pattern for `deal` and `funding`. Each gets its corresponding `TenderPreferences`/`DealPreferences`/`FundingPreferences` struct in `prefs.go`, even though the stub doesn't read all fields — they exist so the onboarding flow has a target type to write into.

- [ ] **Step 2: Register all five matchers at app boot**

In `apps/matching/cmd/main.go`:

```go
matcherReg := matchers.NewRegistry()
matcherReg.Register(jobm.New())
matcherReg.Register(scholarshipm.New())
matcherReg.Register(tenderm.New())
matcherReg.Register(dealm.New())
matcherReg.Register(fundingm.New())
util.Log(ctx).WithField("matchers", matcherReg.Kinds()).Info("matcher registry: loaded")
```

- [ ] **Step 3: Test**

```bash
go test ./apps/matching/...
```

- [ ] **Step 4: Commit**

```bash
git add apps/matching/service/matchers/{tender,deal,funding}/ apps/matching/cmd/main.go
git commit -m "feat(matching): tender/deal/funding stub matchers + boot-time registration"
```

### Task 7.6: Polymorphic preferences event

**Files:**
- Modify: `pkg/events/v1/candidates.go`
- Modify: candidate-side handlers consuming `PreferencesUpdatedV1`

- [ ] **Step 1: Reshape PreferencesUpdatedV1**

```go
// pkg/events/v1/candidates.go — replace existing PreferencesUpdatedV1
type PreferencesUpdatedV1 struct {
	CandidateID string                     `json:"candidate_id"`
	OptIns      map[string]json.RawMessage `json:"opt_ins"`
	UpdatedAt   time.Time                  `json:"updated_at"`
}
```

- [ ] **Step 2: Migrate consumers**

Producers/consumers of the old shape need updating. Inventory:

```bash
grep -rn "PreferencesUpdatedV1" --include='*.go'
```

For each consumer, replace `evt.SalaryMin` / `evt.TargetRoles` etc. with reading the relevant kind blob via:

```go
raw, ok := evt.OptIns["job"]
if !ok {
	return nil // candidate hasn't opted into jobs
}
var p job.JobPreferences
if err := json.Unmarshal(raw, &p); err != nil {
	return err
}
```

- [ ] **Step 3: Test**

```bash
go build ./...
go test ./...
```

- [ ] **Step 4: Commit**

```bash
git add pkg/events/v1/candidates.go apps/matching/
git commit -m "feat(candidates): PreferencesUpdatedV1 carries OptIns map[kind]RawMessage"
```

### Task 7.7: Per-kind onboarding flows (UI)

**Files:**
- Create: `ui/app/src/onboarding/shared/LocationPicker.tsx`
- Create: `ui/app/src/onboarding/shared/AmountRange.tsx`
- Create: `ui/app/src/onboarding/job/v1/Flow.tsx`
- Create: `ui/app/src/onboarding/scholarship/v1/Flow.tsx`
- Create: `ui/app/src/onboarding/tender/v1/Flow.tsx`
- Create: `ui/app/src/onboarding/deal/v1/Flow.tsx`
- Create: `ui/app/src/onboarding/funding/v1/Flow.tsx`
- Create: `ui/app/src/onboarding/router.tsx`

- [ ] **Step 1: Shared LocationPicker**

```tsx
// ui/app/src/onboarding/shared/LocationPicker.tsx
import { useState } from "react";

export interface LocationPreference {
  countries: string[];
  regions: string[];
  cities: string[];
  near_lat?: number;
  near_lon?: number;
  radius_km?: number;
  remote_ok: boolean;
}

export function LocationPicker({
  value,
  onChange,
  label = "Locations",
}: {
  value: LocationPreference;
  onChange: (next: LocationPreference) => void;
  label?: string;
}) {
  return (
    <div class="space-y-3">
      <label class="text-sm font-medium">{label}</label>
      <input
        type="text"
        placeholder="Countries (comma-separated ISO codes, e.g. KE, TZ, UG)"
        value={value.countries.join(", ")}
        onInput={(e) =>
          onChange({
            ...value,
            countries: (e.currentTarget as HTMLInputElement).value
              .split(",")
              .map((s) => s.trim().toUpperCase())
              .filter(Boolean),
          })
        }
        class="input"
      />
      <input
        type="text"
        placeholder="Cities (comma-separated)"
        value={value.cities.join(", ")}
        onInput={(e) =>
          onChange({
            ...value,
            cities: (e.currentTarget as HTMLInputElement).value
              .split(",")
              .map((s) => s.trim())
              .filter(Boolean),
          })
        }
        class="input"
      />
      <label class="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          checked={value.remote_ok}
          onChange={(e) =>
            onChange({ ...value, remote_ok: (e.currentTarget as HTMLInputElement).checked })
          }
        />
        Open to remote
      </label>
    </div>
  );
}
```

- [ ] **Step 2: Shared AmountRange**

```tsx
// ui/app/src/onboarding/shared/AmountRange.tsx
import { JSX } from "preact";

export function AmountRange({
  label,
  currency,
  amountMin,
  amountMax,
  onAmountMin,
  onAmountMax,
  onCurrency,
}: {
  label: string;
  currency: string;
  amountMin: number;
  amountMax: number;
  onAmountMin: (v: number) => void;
  onAmountMax: (v: number) => void;
  onCurrency: (v: string) => void;
}): JSX.Element {
  return (
    <div class="space-y-2">
      <label class="text-sm font-medium">{label}</label>
      <div class="flex gap-2">
        <input
          type="number"
          placeholder="min"
          value={amountMin || ""}
          onInput={(e) => onAmountMin(Number((e.currentTarget as HTMLInputElement).value))}
          class="input flex-1"
        />
        <input
          type="number"
          placeholder="max"
          value={amountMax || ""}
          onInput={(e) => onAmountMax(Number((e.currentTarget as HTMLInputElement).value))}
          class="input flex-1"
        />
        <input
          type="text"
          placeholder="USD"
          value={currency}
          onInput={(e) => onCurrency((e.currentTarget as HTMLInputElement).value.toUpperCase())}
          class="input w-20"
        />
      </div>
    </div>
  );
}
```

- [ ] **Step 3: Job onboarding flow (full implementation)**

```tsx
// ui/app/src/onboarding/job/v1/Flow.tsx
import { useState } from "react";
import { LocationPicker, type LocationPreference } from "@/onboarding/shared/LocationPicker";
import { AmountRange } from "@/onboarding/shared/AmountRange";

export interface JobPreferences {
  target_roles: string[];
  employment_types: string[];
  seniority_levels: string[];
  salary_min: number;
  salary_max: number;
  currency: string;
  locations: LocationPreference;
}

const EMPTY: JobPreferences = {
  target_roles: [],
  employment_types: [],
  seniority_levels: [],
  salary_min: 0,
  salary_max: 0,
  currency: "USD",
  locations: { countries: [], regions: [], cities: [], remote_ok: false },
};

export function Flow({ initial, onSubmit }: { initial?: JobPreferences; onSubmit: (p: JobPreferences) => void }) {
  const [p, setP] = useState<JobPreferences>(initial ?? EMPTY);
  return (
    <form onSubmit={(e) => { e.preventDefault(); onSubmit(p); }} class="space-y-6 max-w-xl">
      <h2 class="text-2xl font-semibold">Tell us about your job search</h2>

      <input
        type="text"
        placeholder="Target roles (comma-separated)"
        value={p.target_roles.join(", ")}
        onInput={(e) => setP({ ...p, target_roles: (e.currentTarget as HTMLInputElement).value.split(",").map((s) => s.trim()).filter(Boolean) })}
        class="input"
      />

      <fieldset>
        <legend class="text-sm font-medium">Employment type</legend>
        {["full-time", "part-time", "contract", "internship", "freelance"].map((t) => (
          <label class="inline-flex items-center gap-2 mr-3" key={t}>
            <input
              type="checkbox"
              checked={p.employment_types.includes(t)}
              onChange={(e) => {
                const checked = (e.currentTarget as HTMLInputElement).checked;
                setP({
                  ...p,
                  employment_types: checked
                    ? [...p.employment_types, t]
                    : p.employment_types.filter((x) => x !== t),
                });
              }}
            />
            {t}
          </label>
        ))}
      </fieldset>

      <AmountRange
        label="Salary range"
        currency={p.currency}
        amountMin={p.salary_min}
        amountMax={p.salary_max}
        onAmountMin={(v) => setP({ ...p, salary_min: v })}
        onAmountMax={(v) => setP({ ...p, salary_max: v })}
        onCurrency={(c) => setP({ ...p, currency: c })}
      />

      <LocationPicker value={p.locations} onChange={(l) => setP({ ...p, locations: l })} />

      <button type="submit" class="btn-primary">Save preferences</button>
    </form>
  );
}
```

- [ ] **Step 4: Scholarship onboarding flow**

```tsx
// ui/app/src/onboarding/scholarship/v1/Flow.tsx
import { useState } from "react";
import { LocationPicker, type LocationPreference } from "@/onboarding/shared/LocationPicker";

export interface ScholarshipPreferences {
  degree_levels: string[];
  fields_of_study: string[];
  nationality: string;
  gpa_min: number;
  locations: LocationPreference;
  deadline_within_days: number;
}

const EMPTY: ScholarshipPreferences = {
  degree_levels: [],
  fields_of_study: [],
  nationality: "",
  gpa_min: 0,
  locations: { countries: [], regions: [], cities: [], remote_ok: false },
  deadline_within_days: 90,
};

export function Flow({ initial, onSubmit }: { initial?: ScholarshipPreferences; onSubmit: (p: ScholarshipPreferences) => void }) {
  const [p, setP] = useState<ScholarshipPreferences>(initial ?? EMPTY);
  return (
    <form onSubmit={(e) => { e.preventDefault(); onSubmit(p); }} class="space-y-6 max-w-xl">
      <h2 class="text-2xl font-semibold">Find scholarships that fit you</h2>

      <fieldset>
        <legend class="text-sm font-medium">Degree level</legend>
        {["undergraduate", "masters", "doctoral", "postdoc"].map((d) => (
          <label class="inline-flex items-center gap-2 mr-3" key={d}>
            <input
              type="checkbox"
              checked={p.degree_levels.includes(d)}
              onChange={(e) => {
                const checked = (e.currentTarget as HTMLInputElement).checked;
                setP({ ...p, degree_levels: checked ? [...p.degree_levels, d] : p.degree_levels.filter((x) => x !== d) });
              }}
            />
            {d}
          </label>
        ))}
      </fieldset>

      <input
        type="text"
        placeholder="Fields of study (comma-separated)"
        value={p.fields_of_study.join(", ")}
        onInput={(e) => setP({ ...p, fields_of_study: (e.currentTarget as HTMLInputElement).value.split(",").map((s) => s.trim()).filter(Boolean) })}
        class="input"
      />

      <input
        type="text"
        placeholder="Your nationality (ISO code, e.g. KE)"
        value={p.nationality}
        onInput={(e) => setP({ ...p, nationality: (e.currentTarget as HTMLInputElement).value.toUpperCase() })}
        class="input"
        maxLength={2}
      />

      <input
        type="number"
        placeholder="Minimum GPA"
        value={p.gpa_min || ""}
        onInput={(e) => setP({ ...p, gpa_min: Number((e.currentTarget as HTMLInputElement).value) })}
        class="input"
        step="0.1"
        min="0"
        max="4"
      />

      <LocationPicker value={p.locations} onChange={(l) => setP({ ...p, locations: l })} label="Where do you want to study?" />

      <button type="submit" class="btn-primary">Save preferences</button>
    </form>
  );
}
```

- [ ] **Step 5: Tender flow**

```tsx
// ui/app/src/onboarding/tender/v1/Flow.tsx
import { useState } from "react";

export interface TenderPreferences {
  company_name: string;
  registration_country: string;
  capabilities: string[];
}

export function Flow({ onSubmit }: { onSubmit: (p: TenderPreferences) => void }) {
  const [p, setP] = useState<TenderPreferences>({ company_name: "", registration_country: "", capabilities: [] });
  return (
    <form onSubmit={(e) => { e.preventDefault(); onSubmit(p); }} class="space-y-4 max-w-xl">
      <h2 class="text-2xl font-semibold">Tell us about your company</h2>
      <input class="input" placeholder="Company name" value={p.company_name}
        onInput={(e) => setP({ ...p, company_name: (e.currentTarget as HTMLInputElement).value })} />
      <input class="input" placeholder="Registration country (ISO code)" value={p.registration_country} maxLength={2}
        onInput={(e) => setP({ ...p, registration_country: (e.currentTarget as HTMLInputElement).value.toUpperCase() })} />
      <input class="input" placeholder="Capabilities (comma-separated)" value={p.capabilities.join(", ")}
        onInput={(e) => setP({ ...p, capabilities: (e.currentTarget as HTMLInputElement).value.split(",").map((s) => s.trim()).filter(Boolean) })} />
      <button class="btn-primary" type="submit">Save preferences</button>
    </form>
  );
}
```

- [ ] **Step 6: Deal flow**

```tsx
// ui/app/src/onboarding/deal/v1/Flow.tsx
import { useState } from "react";

export interface DealPreferences {
  interest_categories: string[];
  countries: string[];
}

export function Flow({ onSubmit }: { onSubmit: (p: DealPreferences) => void }) {
  const [p, setP] = useState<DealPreferences>({ interest_categories: [], countries: [] });
  const CATEGORIES = ["Software", "Hardware", "Travel", "Education", "Subscriptions"];
  return (
    <form onSubmit={(e) => { e.preventDefault(); onSubmit(p); }} class="space-y-4 max-w-xl">
      <h2 class="text-2xl font-semibold">What kinds of deals interest you?</h2>
      <fieldset>
        <legend class="text-sm font-medium">Categories</legend>
        {CATEGORIES.map((c) => (
          <label class="inline-flex items-center gap-2 mr-3" key={c}>
            <input type="checkbox" checked={p.interest_categories.includes(c)}
              onChange={(e) => {
                const checked = (e.currentTarget as HTMLInputElement).checked;
                setP({ ...p, interest_categories: checked ? [...p.interest_categories, c] : p.interest_categories.filter((x) => x !== c) });
              }} />
            {c}
          </label>
        ))}
      </fieldset>
      <input class="input" placeholder="Countries (comma-separated ISO codes)" value={p.countries.join(", ")}
        onInput={(e) => setP({ ...p, countries: (e.currentTarget as HTMLInputElement).value.split(",").map((s) => s.trim().toUpperCase()).filter(Boolean) })} />
      <button class="btn-primary" type="submit">Save preferences</button>
    </form>
  );
}
```

- [ ] **Step 7: Funding flow**

```tsx
// ui/app/src/onboarding/funding/v1/Flow.tsx
import { useState } from "react";
import { LocationPicker, type LocationPreference } from "@/onboarding/shared/LocationPicker";
import { AmountRange } from "@/onboarding/shared/AmountRange";

export interface FundingPreferences {
  organisation_type: string;
  focus_areas: string[];
  geographic_scope: LocationPreference;
  funding_amount_needed_min: number;
  funding_amount_needed_max: number;
  currency: string;
}

const EMPTY: FundingPreferences = {
  organisation_type: "nonprofit",
  focus_areas: [],
  geographic_scope: { countries: [], regions: [], cities: [], remote_ok: false },
  funding_amount_needed_min: 0,
  funding_amount_needed_max: 0,
  currency: "USD",
};

export function Flow({ initial, onSubmit }: { initial?: FundingPreferences; onSubmit: (p: FundingPreferences) => void }) {
  const [p, setP] = useState<FundingPreferences>(initial ?? EMPTY);
  return (
    <form onSubmit={(e) => { e.preventDefault(); onSubmit(p); }} class="space-y-6 max-w-xl">
      <h2 class="text-2xl font-semibold">Tell us about your funding needs</h2>
      <select class="input" value={p.organisation_type}
        onChange={(e) => setP({ ...p, organisation_type: (e.currentTarget as HTMLSelectElement).value })}>
        <option value="nonprofit">Nonprofit</option>
        <option value="for_profit">For-profit</option>
        <option value="individual">Individual</option>
      </select>
      <input class="input" placeholder="Focus areas (comma-separated, e.g. Climate, Education)" value={p.focus_areas.join(", ")}
        onInput={(e) => setP({ ...p, focus_areas: (e.currentTarget as HTMLInputElement).value.split(",").map((s) => s.trim()).filter(Boolean) })} />
      <AmountRange label="Funding amount needed"
        currency={p.currency} amountMin={p.funding_amount_needed_min} amountMax={p.funding_amount_needed_max}
        onAmountMin={(v) => setP({ ...p, funding_amount_needed_min: v })}
        onAmountMax={(v) => setP({ ...p, funding_amount_needed_max: v })}
        onCurrency={(c) => setP({ ...p, currency: c })} />
      <LocationPicker value={p.geographic_scope} onChange={(l) => setP({ ...p, geographic_scope: l })}
        label="Geographic scope" />
      <button class="btn-primary" type="submit">Save preferences</button>
    </form>
  );
}
```

- [ ] **Step 8: Onboarding router**

```tsx
// ui/app/src/onboarding/router.tsx
import { lazy, Suspense } from "preact/compat";

const flows: Record<string, () => Promise<{ Flow: any }>> = {
  "job-onboarding-v1":         () => import("./job/v1/Flow"),
  "scholarship-onboarding-v1": () => import("./scholarship/v1/Flow"),
  "tender-onboarding-v1":      () => import("./tender/v1/Flow"),
  "deal-onboarding-v1":        () => import("./deal/v1/Flow"),
  "funding-onboarding-v1":     () => import("./funding/v1/Flow"),
};

export function OnboardingRouter({ flowId, onSubmit }: { flowId: string; onSubmit: (p: any) => void }) {
  const loader = flows[flowId];
  if (!loader) return <div>Unknown onboarding flow: {flowId}</div>;
  const LazyFlow = lazy(async () => ({ default: (await loader()).Flow }));
  return <Suspense fallback={<div>Loading…</div>}><LazyFlow onSubmit={onSubmit} /></Suspense>;
}
```

- [ ] **Step 9: Wire the router into the dashboard**

Find the existing dashboard / preferences page (`ui/app/src/pages/Dashboard.tsx` or similar). Replace the single existing job-prefs form with kind tabs:

```tsx
import { useState } from "react";
import { OnboardingRouter } from "@/onboarding/router";

const KINDS = [
  { kind: "job", flow: "job-onboarding-v1", label: "Jobs" },
  { kind: "scholarship", flow: "scholarship-onboarding-v1", label: "Scholarships" },
  { kind: "tender", flow: "tender-onboarding-v1", label: "Tenders" },
  { kind: "deal", flow: "deal-onboarding-v1", label: "Deals" },
  { kind: "funding", flow: "funding-onboarding-v1", label: "Funding" },
];

export function Dashboard() {
  const [active, setActive] = useState<string>("job");
  const [optIns, setOptIns] = useState<Record<string, any>>({});
  return (
    <div>
      <nav class="flex gap-2 border-b mb-6">
        {KINDS.map(({ kind, label }) => (
          <button
            key={kind}
            class={`px-4 py-2 ${active === kind ? "border-b-2 border-accent-400" : ""}`}
            onClick={() => setActive(kind)}
          >
            {label}
          </button>
        ))}
      </nav>
      <OnboardingRouter
        flowId={KINDS.find((k) => k.kind === active)!.flow}
        onSubmit={(p) => persistOptIn(active, p, setOptIns)}
      />
    </div>
  );
}

async function persistOptIn(kind: string, prefs: unknown, setOptIns: (f: any) => void) {
  setOptIns((cur: any) => ({ ...cur, [kind]: prefs }));
  await fetch("/api/v1/me/preferences", {
    method: "PUT",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ opt_ins: { [kind]: prefs } }),
  });
}
```

- [ ] **Step 10: Smoke test in browser**

```bash
make ui-dev
# open http://localhost:1313/dashboard, click each tab, save a preference, reload, verify it persists
```

- [ ] **Step 11: Commit**

```bash
git add ui/app/src/onboarding/ ui/app/src/pages/Dashboard.tsx
git commit -m "feat(ui): per-kind onboarding flows + dashboard tab per kind"
```

### Phase 7 smoke check

```bash
go build ./...
go test ./...
make ui-build
```

Expected: green Go build, all tests pass, UI builds. Five matchers registered at boot, polymorphic preferences flow through Frame, dashboard renders five tabs.

---

## Phase 8 — Pilot non-job kind

Outcome: One scholarship source registered end-to-end. Validates the architecture under live data.

### Task 8.1: DAAD scholarship connector

**Files:**
- Create: `pkg/connectors/daad/connector.go`
- Test: `pkg/connectors/daad/connector_test.go`
- Create: `apps/seeds/scholarships-daad.yaml`

- [ ] **Step 1: Author the seed**

```yaml
# apps/seeds/scholarships-daad.yaml
id: daad-scholarships
type: GenericHTML
url: https://www.daad.de/en/study-and-research-in-germany/scholarships/
kinds: [scholarship]
required_attributes_by_kind:
  scholarship: [deadline, field_of_study]
```

- [ ] **Step 2: Build the connector**

Pattern after `pkg/connectors/remoteok/remoteok.go`. The DAAD listing pages return HTML; the connector uses the `extractor` to classify+extract per page. Each `ExternalOpportunity` is tagged `Kind: "scholarship"`.

- [ ] **Step 3: Capture a real DAAD page as a testdata fixture**

```bash
mkdir -p pkg/connectors/daad/testdata
curl -sSL https://www.daad.de/en/study-and-research-in-germany/scholarships/ \
    > pkg/connectors/daad/testdata/listing.html
# Pick one detail page and capture it too:
curl -sSL "<one detail URL from the listing>" \
    > pkg/connectors/daad/testdata/detail.html
```

- [ ] **Step 4: End-to-end test**

```go
// pkg/connectors/daad/connector_test.go
package daad

import (
	"context"
	_ "embed"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
)

//go:embed testdata/detail.html
var detailHTML string

func TestDAAD_DetailExtractsScholarship(t *testing.T) {
	reg, err := opportunity.LoadFromDir("../../../definitions/opportunity-kinds")
	if err != nil {
		t.Fatal(err)
	}
	ext := extraction.New(realLLM(t), reg)
	opp, err := ext.Extract(context.Background(), detailHTML, []string{"scholarship"})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if opp.Kind != "scholarship" {
		t.Errorf("Kind=%q, want scholarship", opp.Kind)
	}
	if opp.Title == "" {
		t.Error("Title empty")
	}
	if _, ok := opp.Attributes["deadline"]; !ok {
		t.Error("missing deadline attribute")
	}
	if _, ok := opp.Attributes["field_of_study"]; !ok {
		t.Error("missing field_of_study attribute")
	}

	src := &domain.Source{Kinds: []string{"scholarship"},
		RequiredAttributesByKind: map[string][]string{"scholarship": {"deadline", "field_of_study"}}}
	res := opportunity.Verify(opp, src, reg)
	if !res.OK {
		t.Fatalf("Verify failed: %+v", res)
	}
}

// realLLM returns the configured production LLM client when env vars are
// present; otherwise the test is skipped. Integration tests against the
// real LLM are gated behind the `integration` build tag in CI.
func realLLM(t *testing.T) extraction.LLM {
	t.Helper()
	llm, err := extraction.NewClientFromEnv()
	if err != nil {
		t.Skipf("LLM env vars not set: %v", err)
	}
	return llm
}
```

- [ ] **Step 5: Register source in cluster**

```bash
# After the seed YAML lands and a deploy ships:
curl -X POST -H 'content-type: application/json' \
    https://api.stawi.org/jobs/_admin/sources \
    -d @apps/seeds/scholarships-daad.yaml
```

- [ ] **Step 6: Verify end-to-end**

- Check `opportunities.variants` Iceberg partition for `kind=scholarship` records
- Check Manticore: `SELECT title, kind FROM idx_opportunities_rt WHERE kind='scholarship' LIMIT 5`
- Check R2: `s3 ls s3://opportunities-content/scholarships/`
- Visit https://opportunities.stawi.org/scholarships/ — listing page should render
- Open one detail page — `opportunity-card.html` renders deadline + field_of_study, no salary

- [ ] **Step 7: Commit**

```bash
git add pkg/connectors/daad/ apps/seeds/scholarships-daad.yaml
git commit -m "feat(connectors): DAAD scholarship connector — first non-job source live"
```

### Phase 8 smoke check

End-to-end through every layer:

- [ ] Scholarship records visible in Iceberg
- [ ] Scholarship records indexed in Manticore
- [ ] Scholarship slugs at `s3://opportunities-content/scholarships/<slug>.json`
- [ ] `opportunities.stawi.org/scholarships/` lists them
- [ ] `opportunities.stawi.org/scholarships/<slug>/` renders detail page with deadline + field of study (no salary)
- [ ] Candidate dashboard's Scholarships tab returns at least one match for a candidate who opted into scholarships with matching field_of_study
- [ ] `pipeline.opportunities.ready{kind="scholarship"}` counter is non-zero in OpenObserve

---

## Final verification (post-Phase 8)

Run the verification checklist from the spec:

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
