# Extraction Recipes — Plan 4: Generator, Validator, Handlers & Config

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`).

**Goal:** Synthesize recipes with AI (the `Generator`), gate them with a dry-run `Validator`, and wire the generate/regenerate lifecycle into Frame events + config so onboarding produces a recipe and drift triggers regeneration — all keeping per-page crawling LLM-free.

**Architecture:** `Generator` (uses the `recipe.LLM` seam — satisfied by `extraction.Extractor`) samples pages, builds a synthesis prompt from `opportunity.Registry` kind schemas, parses the LLM's JSON into a `Recipe`, and repairs on validation failure. `Validator` dry-runs `buildOpportunity` + `opportunity.Verify` on the samples for a pass-rate gate. Frame handlers run generate→validate→`RecipeRepository.Activate`; `page_completed_handler` emits a regenerate event on sustained drift.

**Tech Stack:** Go 1.26, `pkg/recipe` (Plans 1-3), `pkg/opportunity`, `pkg/extraction`, Frame events, `pkg/repository.RecipeRepository`.

**Spec:** design §5C (Generator), §5D (Validator), §5F (handlers), §5H (config). **Depends on:** Plans 1-3.

**Key facts (from infra exploration):**
- `recipe.LLM` seam target: `extraction.Extractor.chat(ctx, prompt, true)` is private → add a public `Complete`.
- `opportunity.Verify(opp *domain.ExternalOpportunity, src *domain.Source, reg *Registry) VerifyResult{OK, Missing, ...}`; `Registry.Resolve(kind) Spec{ExtractionPrompt, KindRequired, KindOptional}`.
- Frame handler shape: implement `events.EventI` (`Name() string`, `PayloadType() any` → `&json.RawMessage`, `Validate(ctx, payload) error`, `Execute(ctx, payload) error`); register in `cmd/main.go` via `frame.WithRegisterEvents(...)`; emit via `svc.EventsManager().Emit(ctx, topic, envelope)`. Topics in `pkg/events/v1/names.go`; envelope `eventsv1.NewEnvelope(topic, payload)`.
- Config: `apps/crawler/config/config.go` env-tagged struct; add `RECIPE_*`.
- Drift hook: `page_completed_handler.go:108` `case rejectRate > 0.8 && p.JobsFound > 0`.

**Cycle note:** `pkg/recipe` may import `pkg/opportunity` (opportunity does not import recipe). `pkg/recipe` already imports `pkg/domain`.

---

## File Structure
| File | Responsibility |
|------|----------------|
| `pkg/recipe/llm.go` | `LLM` interface + `SamplePage` type |
| `pkg/recipe/prompts.go` | recipe-synthesis + repair prompt builders |
| `pkg/recipe/generator.go` | `Generator`: sample → prompt → parse → repair |
| `pkg/recipe/validator.go` | `ValidateRecipe`: dry-run + Verify → pass-rate report |
| `pkg/extraction/extractor.go` (modify) | public `Complete` method |
| `apps/crawler/config/config.go` (modify) | `RECIPE_*` knobs |
| `pkg/events/v1/recipe.go` + `names.go` (modify) | recipe event payloads + topics |
| `apps/crawler/service/recipe_handlers.go` | generate + regenerate Frame handlers |
| `apps/crawler/service/page_completed_handler.go` (modify) | emit regenerate on drift |
| `apps/crawler/cmd/main.go` (modify) | construct + register handlers |

---

## Task 1: `LLM` seam, prompts, `Generator`

**Files:** Create `pkg/recipe/llm.go`, `pkg/recipe/prompts.go`, `pkg/recipe/generator.go`, `pkg/recipe/generator_test.go`.

- [ ] **Step 1: Failing test** `pkg/recipe/generator_test.go`

```go
package recipe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLLM returns a canned response per call, advancing through `replies`.
type fakeLLM struct {
	replies []string
	calls   int
	lastPrompt string
}

func (f *fakeLLM) Complete(_ context.Context, prompt string) (string, error) {
	f.lastPrompt = prompt
	r := f.replies[min(f.calls, len(f.replies)-1)]
	f.calls++
	return r, nil
}

func minimalRegistry(t *testing.T) *opportunity.Registry {
	t.Helper()
	reg, err := opportunity.LoadFromDir("../../definitions/opportunity-kinds")
	if err != nil {
		t.Skipf("opportunity kinds not loadable: %v", err)
	}
	return reg
}

const validRecipeJSON = `{"acquisition":"structured_data","kind":{"mode":"source_default"},
"list":{"mode":"selector","item_selector":".card","link":{"from":["selector"],"selector":"a","attr":"href","transform":["absolute_url"]},"pagination":{"mode":"none"}},
"detail":{"record_source":"json_ld","title":{"from":["json_ld"],"json_path":"$.title"},"description":{"from":["json_ld"],"json_path":"$.description"},"issuing_entity":{"from":["json_ld"],"json_path":"$.hiringOrganization.name"},"apply_url":{"from":["json_ld"],"json_path":"$.url"},"anchor_country":{"from":["json_ld"],"json_path":"$.jobLocation.address.addressCountry"}}}`

func genSource() domain.Source {
	s := domain.Source{Type: "brightermonday", BaseURL: "https://x.io", Country: "KE"}
	s.ID = "g1"
	s.Kinds = []string{"job"}
	return s
}

func TestGenerator_ProducesValidRecipe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><head><script type="application/ld+json">{"title":"x"}</script></head><body></body></html>`))
	}))
	defer srv.Close()

	llm := &fakeLLM{replies: []string{"```json\n" + validRecipeJSON + "\n```"}}
	g := NewGenerator(llm, httptestFetcher{client: srv.Client()}, minimalRegistry(t), 3)

	rec, samples, err := g.Generate(context.Background(), genSource(), []string{srv.URL})
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Equal(t, "structured_data", rec.Acquisition)
	assert.NoError(t, rec.Validate())
	require.Len(t, samples, 1)
	// The prompt must mention the target kind's schema (so generation is kind-aware).
	assert.Contains(t, llm.lastPrompt, "job")
}

func TestGenerator_RepairsAfterInvalidThenValid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body></body></html>`))
	}))
	defer srv.Close()

	// First reply is structurally invalid (bad acquisition); second is valid.
	bad := `{"acquisition":"telepathy","kind":{"mode":"source_default"},"list":{"mode":"selector","pagination":{"mode":"none"}},"detail":{}}`
	llm := &fakeLLM{replies: []string{bad, validRecipeJSON}}
	g := NewGenerator(llm, httptestFetcher{client: srv.Client()}, minimalRegistry(t), 3)

	rec, _, err := g.Generate(context.Background(), genSource(), []string{srv.URL})
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Equal(t, 2, llm.calls) // repaired on the second attempt
}

func TestGenerator_FailsAfterMaxAttempts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html></html>`))
	}))
	defer srv.Close()
	llm := &fakeLLM{replies: []string{`{"acquisition":"nope"}`}}
	g := NewGenerator(llm, httptestFetcher{client: srv.Client()}, minimalRegistry(t), 2)
	_, _, err := g.Generate(context.Background(), genSource(), []string{srv.URL})
	require.Error(t, err)
	assert.Equal(t, 2, llm.calls)
}

func TestGenerator_NoFetchableSamples(t *testing.T) {
	llm := &fakeLLM{replies: []string{validRecipeJSON}}
	g := NewGenerator(llm, httptestFetcher{client: http.DefaultClient}, minimalRegistry(t), 2)
	_, _, err := g.Generate(context.Background(), genSource(), []string{"http://127.0.0.1:0/nope"})
	require.Error(t, err)
	assert.Zero(t, llm.calls) // never call the LLM with no samples
}
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./pkg/recipe/ -run TestGenerator` → undefined NewGenerator. (`httptestFetcher` exists in `executor_api_test.go`.)

- [ ] **Step 3: Implement** `pkg/recipe/llm.go`

```go
package recipe

import "context"

// LLM is the text-completion seam the Generator uses. extraction.Extractor
// satisfies it via Complete(ctx, prompt) returning the model's text (JSON mode).
type LLM interface {
	Complete(ctx context.Context, prompt string) (string, error)
}

// SamplePage is one fetched sample used for generation and validation.
type SamplePage struct {
	URL  string
	HTML string
}
```

- [ ] **Step 4: Implement** `pkg/recipe/prompts.go`

```go
package recipe

import (
	"fmt"
	"strings"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
)

const recipeSynthInstructions = `You design DETERMINISTIC web-extraction recipes. Given sample pages from ONE
job/opportunity source, output a single JSON "recipe" that a non-AI executor
will run on every page of this source — so AI runs once, here, not per page.

Output ONLY the JSON recipe object. No prose, no code fences.

A recipe has this shape:
{
  "acquisition": "api" | "structured_data" | "selectors",
  "kind": {"mode":"source_default"|"fixed"|"by_path", "fixed":"<kind>", "path":<extractor>},
  "list": {"mode":"api"|"sitemap"|"structured_data"|"selector",
           "endpoint":"/path", "items_path":"$.jsonpath", "item_selector":".css",
           "link":<extractor>, "pagination":{"mode":"none"|"page_param"|"cursor"|"next_link","param":"page","next":<extractor>,"cursor":<extractor>,"max_pages":10}},
  "detail": {"record_source":"api"|"json_ld"|"next_data"|"microdata"|"html",
             "title":<extractor>, "description":<extractor>, "issuing_entity":<extractor>,
             "apply_url":<extractor>, "anchor_country":<extractor>, "location_text":<extractor>,
             "remote":<extractor>, "posted_at":<extractor>, "deadline":<extractor>,
             "amount_min":<extractor>, "amount_max":<extractor>, "currency":<extractor>,
             "categories":<extractor>, "company_logo_url":<extractor>, "company_profile":<extractor>,
             "attributes": {"<kind_attr>":<extractor>}}
}

An <extractor> resolves ONE value by trying sources in order (first non-empty wins):
{"from":["json_ld","next_data","microdata","selector","meta","record","const"],
 "json_path":"$.x",      // for json_ld/next_data/record
 "microdata":"itemprop", "selector":".css", "attr":"href",
 "meta":"og:image", "const":"literal",
 "transform":["trim","lower","collapse_ws","html_to_text","absolute_url","parse_money","parse_date"]}

Rules:
- PREFER structured data: if pages embed schema.org JSON-LD (JobPosting etc.) or __NEXT_DATA__, map json_path; use CSS selectors only when needed.
- title, description, issuing_entity, apply_url, anchor_country are REQUIRED — always provide an extractor.
- Dates: add "parse_date" transform. Money fields: "parse_money". Relative links: "absolute_url".
- Find the listing/detail structure: item_selector picks each card on the listing; link extracts each card's detail URL.
- Output VALID JSON that parses into the schema above.`

// buildGenerationPrompt assembles the synthesis prompt: instructions + target
// kind schema(s) + sample pages (truncated, structured-data preserved).
func (g *Generator) buildGenerationPrompt(src domain.Source, samples []SamplePage) string {
	var b strings.Builder
	b.WriteString(recipeSynthInstructions)

	b.WriteString("\n\nTarget opportunity kind(s) for THIS source and their field schemas:\n")
	for _, k := range []string(src.Kinds) {
		spec := g.reg.Resolve(k)
		b.WriteString(fmt.Sprintf("\n--- kind=%s ---\n", k))
		if spec.ExtractionPrompt != "" {
			b.WriteString(spec.ExtractionPrompt)
			b.WriteString("\n")
		}
		if len(spec.KindRequired) > 0 {
			b.WriteString("Required attributes: " + strings.Join(spec.KindRequired, ", ") + "\n")
		}
		if len(spec.KindOptional) > 0 {
			b.WriteString("Optional attributes: " + strings.Join(spec.KindOptional, ", ") + "\n")
		}
	}

	b.WriteString("\nSample pages from this source:\n")
	for i, s := range samples {
		b.WriteString(fmt.Sprintf("\n=== SAMPLE %d: %s ===\n", i+1, s.URL))
		b.WriteString(truncate(s.HTML, g.sampleChars))
		b.WriteString("\n")
	}
	return b.String()
}

func (g *Generator) repairPrompt(base, errMsg string) string {
	return base + "\n\nYour previous output was rejected: " + errMsg +
		"\nReturn a corrected JSON recipe that fixes this. Output ONLY the JSON."
}

func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n]
}

var _ = opportunity.Spec{} // ensure opportunity import is used even if Resolve inlines
```

- [ ] **Step 5: Implement** `pkg/recipe/generator.go`

```go
package recipe

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
)

// Generator synthesizes an extraction recipe for a source from sample pages,
// using the LLM once (with a bounded repair loop). It is the ONLY AI consumer;
// the resulting recipe runs LLM-free thereafter.
type Generator struct {
	llm         LLM
	fetcher     Fetcher
	reg         *opportunity.Registry
	maxAttempts int
	sampleChars int
}

// NewGenerator builds a Generator. maxAttempts bounds the parse/validate repair
// loop (>=1). Sample HTML is truncated to a sane size for the prompt.
func NewGenerator(llm LLM, f Fetcher, reg *opportunity.Registry, maxAttempts int) *Generator {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	return &Generator{llm: llm, fetcher: f, reg: reg, maxAttempts: maxAttempts, sampleChars: 6000}
}

// Generate fetches the sample URLs, asks the LLM to synthesize a recipe, and
// repairs it (bounded) until it parses and structurally validates. It returns
// the recipe plus the fetched samples (for the Validator's pass-rate gate).
func (g *Generator) Generate(ctx context.Context, src domain.Source, sampleURLs []string) (*Recipe, []SamplePage, error) {
	var samples []SamplePage
	for _, u := range sampleURLs {
		body, status, err := g.fetcher.Get(ctx, u)
		if err != nil || status < 200 || status >= 300 {
			continue
		}
		samples = append(samples, SamplePage{URL: u, HTML: string(body)})
	}
	if len(samples) == 0 {
		return nil, nil, fmt.Errorf("recipe generation: no fetchable samples among %d URLs", len(sampleURLs))
	}

	prompt := g.buildGenerationPrompt(src, samples)
	var lastErr error
	for attempt := 0; attempt < g.maxAttempts; attempt++ {
		raw, err := g.llm.Complete(ctx, prompt)
		if err != nil {
			lastErr = err
			continue
		}
		rec, perr := parseRecipeJSON(raw)
		if perr != nil {
			lastErr = perr
			prompt = g.repairPrompt(prompt, perr.Error())
			continue
		}
		if verr := rec.Validate(); verr != nil {
			lastErr = verr
			prompt = g.repairPrompt(prompt, verr.Error())
			continue
		}
		return rec, samples, nil
	}
	return nil, samples, fmt.Errorf("recipe generation failed after %d attempts: %w", g.maxAttempts, lastErr)
}

// parseRecipeJSON strips markdown fences/prose and unmarshals into a Recipe.
func parseRecipeJSON(raw string) (*Recipe, error) {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "```") {
		s = s[3:]
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			s = s[nl+1:]
		}
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
	}
	if i := strings.IndexAny(s, "{["); i > 0 {
		s = s[i:]
	}
	s = strings.TrimSpace(s)
	var rec Recipe
	if err := json.Unmarshal([]byte(s), &rec); err != nil {
		return nil, fmt.Errorf("recipe JSON parse: %w", err)
	}
	return &rec, nil
}
```

- [ ] **Step 6: Run, expect PASS** — `go test ./pkg/recipe/ -run TestGenerator -v`. If `opportunity.LoadFromDir` path is wrong, find the real kinds dir (search `LoadFromDir(` usages) and fix the test helper. Then `go test ./pkg/recipe/... && go vet ./pkg/recipe/...`.

- [ ] **Step 7: Commit**

```bash
git add pkg/recipe/llm.go pkg/recipe/prompts.go pkg/recipe/generator.go pkg/recipe/generator_test.go
git commit -m "feat(recipe): AI Generator (sample -> prompt -> parse -> bounded repair) with LLM seam"
```

---

## Task 2: `Validator` (dry-run pass-rate gate)

**Files:** Create `pkg/recipe/validator.go`, `pkg/recipe/validator_test.go`.

- [ ] **Step 1: Failing test** `pkg/recipe/validator_test.go`

```go
package recipe

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateRecipe_PassRate(t *testing.T) {
	reg := minimalRegistry(t)
	// A recipe that extracts the required fields from JSON-LD.
	jl := func(p string) FieldExtractor { return FieldExtractor{From: []string{"json_ld"}, JSONPath: p} }
	rec := &Recipe{Acquisition: "structured_data", Kind: KindRule{Mode: "source_default"},
		List:   ListRule{Mode: "selector", ItemSelector: ".c", Link: FieldExtractor{From: []string{"selector"}, Selector: "a", Attr: "href"}, Pagination: Pagination{Mode: "none"}},
		Detail: DetailRule{RecordSource: "json_ld", Title: jl("$.title"), Description: jl("$.description"),
			IssuingEntity: jl("$.hiringOrganization.name"), ApplyURL: jl("$.url"), AnchorCountry: jl("$.jobLocation.address.addressCountry")}}

	good := SamplePage{URL: "https://x.io/1", HTML: `<html><head><script type="application/ld+json">{"title":"Go Eng","description":"A description that comfortably exceeds the fifty character verify minimum gate.","hiringOrganization":{"name":"ACME"},"url":"https://x.io/apply","jobLocation":{"address":{"addressCountry":"KE"}}}</script></head><body></body></html>`}
	bad := SamplePage{URL: "https://x.io/2", HTML: `<html><body>no structured data here</body></html>`}

	src := genSource()
	rep := ValidateRecipe(rec, src, []SamplePage{good, bad}, reg)
	assert.Equal(t, 2, rep.Samples)
	assert.Equal(t, 1, rep.Passed)
	assert.InDelta(t, 0.5, rep.PassRate, 0.001)
	require.Len(t, rep.PerSample, 2)
	assert.True(t, rep.PerSample[0].OK)
	assert.False(t, rep.PerSample[1].OK)
}
```

- [ ] **Step 2: Run, expect FAIL** — undefined ValidateRecipe.

- [ ] **Step 3: Implement** `pkg/recipe/validator.go`

```go
package recipe

import (
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
)

// SampleResult is one sample's validation outcome.
type SampleResult struct {
	URL     string   `json:"url"`
	OK      bool     `json:"ok"`
	Missing []string `json:"missing,omitempty"`
}

// ValidationReport summarizes a recipe dry-run over sample pages. It mirrors the
// shape stored in source_recipes.validation_report.
type ValidationReport struct {
	Samples   int            `json:"samples"`
	Passed    int            `json:"passed"`
	PassRate  float64        `json:"pass_rate"`
	PerSample []SampleResult `json:"per_sample,omitempty"`
}

// ValidateRecipe dry-runs the recipe's detail extraction over each sample page
// and runs opportunity.Verify on the result, producing a pass-rate. This is the
// quality gate: a recipe is accepted only when PassRate clears a threshold.
// (Detail-page samples cover structured_data/selectors recipes — the ones that
// need validation; api-mode recipes hit official APIs and are high-confidence.)
func ValidateRecipe(rec *Recipe, src domain.Source, samples []SamplePage, reg *opportunity.Registry) ValidationReport {
	rep := ValidationReport{Samples: len(samples)}
	for _, s := range samples {
		res := SampleResult{URL: s.URL}
		pc, err := NewPageContext(s.URL, s.HTML, nil)
		if err == nil {
			if opp, berr := buildOpportunity(pc, src, rec); berr == nil {
				v := opportunity.Verify(&opp, &src, reg)
				res.OK = v.OK
				res.Missing = v.Missing
			}
		}
		if res.OK {
			rep.Passed++
		}
		rep.PerSample = append(rep.PerSample, res)
	}
	if rep.Samples > 0 {
		rep.PassRate = float64(rep.Passed) / float64(rep.Samples)
	}
	return rep
}
```

- [ ] **Step 4: Run, expect PASS** — `go test ./pkg/recipe/ -run TestValidateRecipe -v`. Then `go test -race ./pkg/recipe/... && go vet ./pkg/recipe/...`.

- [ ] **Step 5: Commit**

```bash
git add pkg/recipe/validator.go pkg/recipe/validator_test.go
git commit -m "feat(recipe): Validator dry-runs recipe + opportunity.Verify for a pass-rate gate"
```

---

## Task 3: `extraction.Complete`, config, events

**Files:** Modify `pkg/extraction/extractor.go`, `apps/crawler/config/config.go`, `pkg/events/v1/names.go`; create `pkg/events/v1/recipe.go`.

- [ ] **Step 1: Add public `Complete` to the Extractor** — in `pkg/extraction/extractor.go`, add (it satisfies `recipe.LLM`):

```go
// Complete sends a JSON-mode chat completion and returns the model's text.
// This exposes the extractor as a recipe.LLM for the recipe Generator.
func (e *Extractor) Complete(ctx context.Context, prompt string) (string, error) {
	return e.chat(ctx, prompt, true)
}
```

- [ ] **Step 2: Add config knobs** — in `apps/crawler/config/config.go`, add to `CrawlerConfig`:

```go
	// Recipe engine (AI-generated per-source extraction recipes).
	RecipeEnabled         bool    `env:"RECIPE_ENABLED" envDefault:"false"`
	RecipeSampleCount     int     `env:"RECIPE_SAMPLE_COUNT" envDefault:"4"`
	RecipePassThreshold   float64 `env:"RECIPE_PASS_THRESHOLD" envDefault:"0.8"`
	RecipeMaxGenAttempts  int     `env:"RECIPE_MAX_GEN_ATTEMPTS" envDefault:"3"`
	RecipeRegenRejectRate float64 `env:"RECIPE_REGEN_REJECT_RATE" envDefault:"0.5"`
	RecipeRegenMinPages   int     `env:"RECIPE_REGEN_MIN_PAGES" envDefault:"20"`
	RecipeMaxRegenFails   int     `env:"RECIPE_MAX_REGEN_FAILURES" envDefault:"3"`
```

- [ ] **Step 3: Add topics** — in `pkg/events/v1/names.go`, add to the const block:

```go
	TopicRecipeGenerate   = "recipe.generate.v1"
	TopicRecipeRegenerate = "recipe.regenerate.v1"
```

- [ ] **Step 4: Add payloads** — create `pkg/events/v1/recipe.go`:

```go
package v1

// RecipeGenerateV1 requests synthesis of a recipe for a source (onboarding).
type RecipeGenerateV1 struct {
	SourceID   string   `json:"source_id"`
	SampleURLs []string `json:"sample_urls,omitempty"` // empty -> handler samples from BaseURL
}

// RecipeRegenerateV1 requests re-synthesis after drift.
type RecipeRegenerateV1 struct {
	SourceID string `json:"source_id"`
	Reason   string `json:"reason,omitempty"`
}
```

- [ ] **Step 5: Build + commit**

Run: `go build ./... && go vet ./pkg/extraction/... ./apps/crawler/config/... ./pkg/events/...`
Expected: clean.

```bash
git add pkg/extraction/extractor.go apps/crawler/config/config.go pkg/events/v1/names.go pkg/events/v1/recipe.go
git commit -m "feat(recipe): expose extractor.Complete (LLM seam), RECIPE_* config, recipe event topics+payloads"
```

---

## Task 4: Generate/Regenerate handlers + drift trigger + wiring

**Files:** Create `apps/crawler/service/recipe_handlers.go`, `apps/crawler/service/recipe_handlers_test.go`; modify `apps/crawler/service/page_completed_handler.go`, `apps/crawler/cmd/main.go`.

- [ ] **Step 1: Failing test** `apps/crawler/service/recipe_handlers_test.go`

```go
package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/recipe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRecipeStore captures Activate calls.
type fakeRecipeStore struct {
	activated *recipe.Recipe
	passRate  float64
}

func (f *fakeRecipeStore) Activate(_ context.Context, _ string, rec *recipe.Recipe, passRate float64, _ string, _ any) error {
	f.activated = rec
	f.passRate = passRate
	return nil
}

type fakeSourceByID struct{ src domain.Source }

func (f fakeSourceByID) GetByID(_ context.Context, _ string) (*domain.Source, error) {
	s := f.src
	return &s, nil
}

type fakeLLM struct{ reply string }

func (f fakeLLM) Complete(_ context.Context, _ string) (string, error) { return f.reply, nil }

func TestRecipeGenerateHandler_GeneratesValidatesActivates(t *testing.T) {
	// Detail page with structured data so generation+validation pass.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><head><script type="application/ld+json">{"title":"Go Eng","description":"A description well over the fifty character verify minimum for sure now.","hiringOrganization":{"name":"ACME"},"url":"https://x.io/apply","jobLocation":{"address":{"addressCountry":"KE"}}}</script></head><body></body></html>`))
	}))
	defer srv.Close()

	reg, err := opportunity.LoadFromDir("../../../definitions/opportunity-kinds")
	if err != nil {
		t.Skipf("kinds: %v", err)
	}

	recipeJSON := `{"acquisition":"structured_data","kind":{"mode":"source_default"},"list":{"mode":"selector","item_selector":".c","link":{"from":["selector"],"selector":"a","attr":"href"},"pagination":{"mode":"none"}},"detail":{"record_source":"json_ld","title":{"from":["json_ld"],"json_path":"$.title"},"description":{"from":["json_ld"],"json_path":"$.description"},"issuing_entity":{"from":["json_ld"],"json_path":"$.hiringOrganization.name"},"apply_url":{"from":["json_ld"],"json_path":"$.url"},"anchor_country":{"from":["json_ld"],"json_path":"$.jobLocation.address.addressCountry"}}}`

	src := domain.Source{Type: "brightermonday", BaseURL: srv.URL, Country: "KE"}
	src.ID = "s1"
	src.Kinds = []string{"job"}

	store := &fakeRecipeStore{}
	gen := recipe.NewGenerator(fakeLLM{reply: recipeJSON}, recipe.NewHTTPFetcher(headeredClient{c: srv.Client()}), reg, 3)
	h := NewRecipeGenerateHandler(RecipeHandlerDeps{
		Sources: fakeSourceByID{src: src}, Recipes: store, Generator: gen, Registry: reg,
		Fetcher: recipe.NewHTTPFetcher(headeredClient{c: srv.Client()}), PassThreshold: 0.8, SampleCount: 2,
	})

	env := eventsv1.NewEnvelope(eventsv1.TopicRecipeGenerate, eventsv1.RecipeGenerateV1{SourceID: "s1", SampleURLs: []string{srv.URL}})
	body, _ := json.Marshal(env)
	raw := json.RawMessage(body)
	require.NoError(t, h.Execute(context.Background(), &raw))

	require.NotNil(t, store.activated)
	assert.Equal(t, "structured_data", store.activated.Acquisition)
	assert.GreaterOrEqual(t, store.passRate, 0.8)
}

// headeredClient adapts an *http.Client to recipe.HeaderedGetter for the test.
type headeredClient struct{ c *http.Client }

func (h headeredClient) Get(ctx context.Context, url string, _ map[string]string) ([]byte, int, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := h.c.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	var b []byte
	tmp := make([]byte, 2048)
	for {
		n, rerr := resp.Body.Read(tmp)
		b = append(b, tmp[:n]...)
		if rerr != nil {
			break
		}
	}
	return b, resp.StatusCode, nil
}
```

- [ ] **Step 2: Run, expect FAIL** — undefined NewRecipeGenerateHandler/RecipeHandlerDeps.

- [ ] **Step 3: Implement** `apps/crawler/service/recipe_handlers.go`

```go
package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pitabwire/util"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/recipe"
)

// RecipeStore is the subset of repository.RecipeRepository the handler needs.
type RecipeStore interface {
	Activate(ctx context.Context, sourceID string, rec *recipe.Recipe, passRate float64, model string, validationReport any) error
}

// SourceByID looks a source up by id (repository.SourceRepository satisfies it).
type SourceByID interface {
	GetByID(ctx context.Context, id string) (*domain.Source, error)
}

// RecipeHandlerDeps wires the generate/regenerate handlers.
type RecipeHandlerDeps struct {
	Sources       SourceByID
	Recipes       RecipeStore
	Generator     *recipe.Generator
	Registry      *opportunity.Registry
	Fetcher       recipe.Fetcher
	Model         string
	PassThreshold float64
	SampleCount   int
}

// RecipeGenerateHandler handles recipe.generate.v1: synthesize, validate, and
// activate a recipe for a source.
type RecipeGenerateHandler struct{ deps RecipeHandlerDeps }

func NewRecipeGenerateHandler(d RecipeHandlerDeps) *RecipeGenerateHandler { return &RecipeGenerateHandler{deps: d} }

func (h *RecipeGenerateHandler) Name() string      { return eventsv1.TopicRecipeGenerate }
func (h *RecipeGenerateHandler) PayloadType() any  { return &json.RawMessage{} }
func (h *RecipeGenerateHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return fmt.Errorf("recipe.generate: empty payload")
	}
	return nil
}

func (h *RecipeGenerateHandler) Execute(ctx context.Context, payload any) error {
	raw, _ := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.RecipeGenerateV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return fmt.Errorf("recipe.generate: decode: %w", err)
	}
	return h.generate(ctx, env.Payload.SourceID, env.Payload.SampleURLs)
}

// generate is shared by the generate and regenerate handlers.
func (h *RecipeGenerateHandler) generate(ctx context.Context, sourceID string, sampleURLs []string) error {
	log := util.Log(ctx)
	src, err := h.deps.Sources.GetByID(ctx, sourceID)
	if err != nil {
		return fmt.Errorf("recipe.generate: source %s: %w", sourceID, err)
	}
	if len(sampleURLs) == 0 {
		sampleURLs = h.sampleURLs(ctx, *src)
	}
	rec, samples, err := h.deps.Generator.Generate(ctx, *src, sampleURLs)
	if err != nil {
		log.WithError(err).WithField("source", sourceID).Warn("recipe.generate: synthesis failed")
		return err // Frame redelivers; bounded by the queue's max attempts
	}
	report := recipe.ValidateRecipe(rec, *src, samples, h.deps.Registry)
	if report.PassRate < h.deps.PassThreshold {
		log.WithField("source", sourceID).WithField("pass_rate", report.PassRate).
			Warn("recipe.generate: below pass threshold; not activating")
		return fmt.Errorf("recipe.generate: pass rate %.2f < %.2f", report.PassRate, h.deps.PassThreshold)
	}
	if err := h.deps.Recipes.Activate(ctx, sourceID, rec, report.PassRate, h.deps.Model, report); err != nil {
		return fmt.Errorf("recipe.generate: activate: %w", err)
	}
	log.WithField("source", sourceID).WithField("pass_rate", report.PassRate).Info("recipe.generate: activated")
	return nil
}

// sampleURLs derives sample detail URLs for a source. For now it samples the
// base URL; richer sampling (listing -> N detail links) is a follow-up.
func (h *RecipeGenerateHandler) sampleURLs(_ context.Context, src domain.Source) []string {
	return []string{src.BaseURL}
}

// RecipeRegenerateHandler handles recipe.regenerate.v1 by re-running generation.
type RecipeRegenerateHandler struct{ gen *RecipeGenerateHandler }

func NewRecipeRegenerateHandler(d RecipeHandlerDeps) *RecipeRegenerateHandler {
	return &RecipeRegenerateHandler{gen: NewRecipeGenerateHandler(d)}
}

func (h *RecipeRegenerateHandler) Name() string     { return eventsv1.TopicRecipeRegenerate }
func (h *RecipeRegenerateHandler) PayloadType() any { return &json.RawMessage{} }
func (h *RecipeRegenerateHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return fmt.Errorf("recipe.regenerate: empty payload")
	}
	return nil
}
func (h *RecipeRegenerateHandler) Execute(ctx context.Context, payload any) error {
	raw, _ := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.RecipeRegenerateV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return fmt.Errorf("recipe.regenerate: decode: %w", err)
	}
	return h.gen.generate(ctx, env.Payload.SourceID, nil)
}
```

- [ ] **Step 4: Run, expect PASS** — `go test ./apps/crawler/service/ -run TestRecipeGenerateHandler -v`. If `Envelope`/`NewEnvelope` generics differ, match `pkg/events/v1/envelope.go`. If `GetByID` on `repository.SourceRepository` returns a different signature, adjust the `SourceByID` interface to match.

- [ ] **Step 5: Drift trigger** — in `apps/crawler/service/page_completed_handler.go`, the `case rejectRate > 0.8 && p.JobsFound > 0` branch currently flags needs_tuning. Add (guarded so it only fires for recipe-driven sources and when the handler has an emitter): after `h.repo.FlagNeedsTuning(...)`, if the handler has been given a recipe-regenerate emitter and `src.ExtractionRecipe` is a non-empty recipe, emit `recipe.regenerate.v1`. To keep this testable and avoid a hard dependency, add an optional field to `PageCompletedHandler` (e.g. `EmitRegenerate func(ctx context.Context, sourceID, reason string)`), set at construction in main.go, and call it here when non-nil and `src.ExtractionRecipe != "" && src.ExtractionRecipe != "{}"`. If wiring an emitter into the existing `NewPageCompletedHandler` signature is more than a mechanical addition, STOP and report DONE_WITH_CONCERNS describing the handler's constructor + how `Svc.EventsManager().Emit` is reachable from it.

- [ ] **Step 6: Wire at boot** — in `apps/crawler/cmd/main.go`, when `cfg.RecipeEnabled` and an extractor exists: construct `recipeGen := recipe.NewGenerator(extractor, recipe.NewHTTPFetcher(httpClient), reg, cfg.RecipeMaxGenAttempts)`, build `RecipeHandlerDeps{Sources: sourceRepo, Recipes: recipeRepo, Generator: recipeGen, Registry: reg, Fetcher: recipe.NewHTTPFetcher(httpClient), Model: cfg.InferenceModel, PassThreshold: cfg.RecipePassThreshold, SampleCount: cfg.RecipeSampleCount}`, and append `NewRecipeGenerateHandler(deps)` + `NewRecipeRegenerateHandler(deps)` to the `handlers` slice passed to `frame.WithRegisterEvents(...)`. Wire the `EmitRegenerate` closure into the page-completed handler using `svc.EventsManager().Emit(ctx, eventsv1.TopicRecipeRegenerate, eventsv1.NewEnvelope(...))`.

- [ ] **Step 7: Build + test + commit**

Run: `go build ./... && go test ./apps/crawler/... ./pkg/recipe/... && go vet ./... && gofmt -l apps/crawler pkg/recipe`
Expected: builds, green, clean.

```bash
git add apps/crawler/service/recipe_handlers.go apps/crawler/service/recipe_handlers_test.go apps/crawler/service/page_completed_handler.go apps/crawler/cmd/main.go
git commit -m "feat(crawler): recipe generate/regenerate handlers + drift-triggered regeneration + boot wiring"
```

---

## Self-Review
- §5C Generator (sample→prompt→parse→repair, kind-aware via Registry, LLM-once) → Task 1. ✓
- §5D Validator (dry-run + Verify pass-rate) → Task 2. ✓
- §5H config (`RECIPE_*`) + LLM seam (`extraction.Complete`) → Task 3. ✓
- §5F handlers (generate/regenerate) + drift trigger + boot wiring → Task 4. ✓
- Placeholder scan: none. Tasks 5/6 of Task 4 include explicit STOP-and-report guards for the two integration points that can't be fully seen from outside (page-completed emitter signature; main wiring).
- Type consistency: `Generator.Generate(ctx, src, urls) (*Recipe, []SamplePage, error)` → consumed by `ValidateRecipe(rec, src, samples, reg) ValidationReport` (Task 2) → consumed by the handler's `generate` (Task 4). `RecipeStore.Activate` matches `repository.RecipeRepository.Activate` signature (Plan 3). `LLM.Complete` matches `extraction.Extractor.Complete` (Task 3). Fake-LLM seam used throughout for tests.
- Note: Generator/Validator tests need the opportunity-kinds dir; the test skips if not loadable. Handler test uses a fake store + fake LLM (no DB, no real inference).
