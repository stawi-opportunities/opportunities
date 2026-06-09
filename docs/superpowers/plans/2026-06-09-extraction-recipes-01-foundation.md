# Extraction Recipes — Plan 1: Deterministic Foundation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the pure, deterministic core of the extraction-recipe engine — the recipe types, a transform registry, a `PageContext` that harvests a page's four data planes, and a `FieldExtractor` evaluation engine — with zero dependency on the database, the LLM, or Frame.

**Architecture:** A new `pkg/recipe` package. The `FieldExtractor` primitive resolves one value by trying an ordered list of sources (structured-data first, selectors last) and piping the result through named transforms. `PageContext` normalizes HTML/JSON-LD/`__NEXT_DATA__`/meta so the extractor reads from one place. Everything here is pure and table-testable against fixtures; the Executor (Plan 2), Store (Plan 3), and Generator (Plan 4) build on it.

**Tech Stack:** Go 1.26, `github.com/PuerkitoBio/goquery` (DOM), `github.com/PaesslerAG/jsonpath` (JSON paths), `github.com/andybalholm/cascadia` (CSS selector validation — already in the module graph via goquery), `github.com/stretchr/testify`.

**Spec:** `docs/superpowers/specs/2026-06-09-ai-generated-extraction-recipes-design.md` (§4 Recipe Schema, §5A PageContext + FieldExtractor engine).

**Plan sequence (this is plan 1 of 5):**
1. **Foundation** (this plan) — types, transforms, PageContext, FieldExtractor engine.
2. Executor — api + html acquisition drivers → `[]ExternalOpportunity`, connector integration.
3. Store — `sources.extraction_recipe` column, `source_recipes` history table, atomic swap/rollback.
4. Generator + Validator + handlers — AI synthesis, validation gate, generate/regenerate Frame handlers, config.
5. Rollout — shadow mode, cutover flag, backfill job, retire legacy path.

---

## File Structure

| File | Responsibility |
|------|----------------|
| `pkg/recipe/recipe.go` | Recipe data types (Recipe, FieldExtractor, ListRule, Pagination, DetailRule, KindRule) + `Recipe.Validate()` |
| `pkg/recipe/transforms.go` | Transform registry + `applyTransforms()` |
| `pkg/recipe/pagecontext.go` | `PageContext` + `NewPageContext()` harvester (JSON-LD, `__NEXT_DATA__`, microdata, meta) |
| `pkg/recipe/fieldeval.go` | `Evaluate()` / `EvaluateList()` — resolve a `FieldExtractor` against a `PageContext` |
| `pkg/recipe/*_test.go` | Table-driven unit tests per file |

> Note: §5A of the spec calls for lifting the structured-data harvester out of `extractor.go:161-183` into shared code. To keep this plan self-contained and avoid touching the live extraction path, we implement a fresh harvester in `pagecontext.go` now; de-duplicating `extractor.go` against it is a cleanup task in Plan 2. Do not modify `extractor.go` in this plan.

---

## Task 1: Recipe types + `Validate()`

**Files:**
- Create: `pkg/recipe/recipe.go`
- Test: `pkg/recipe/recipe_test.go`

- [ ] **Step 1: Write the failing test**

```go
// pkg/recipe/recipe_test.go
package recipe

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validRecipe() *Recipe {
	req := func(from, path string) FieldExtractor {
		return FieldExtractor{From: []string{from}, JSONPath: path, Required: true}
	}
	return &Recipe{
		Version:     1,
		Acquisition: "structured_data",
		Kind:        KindRule{Mode: "source_default"},
		List: ListRule{
			Mode:         "selector",
			ItemSelector: ".job-card",
			Link:         FieldExtractor{From: []string{"selector"}, Selector: "a", Attr: "href", Transform: []string{"absolute_url"}},
			Pagination:   Pagination{Mode: "none"},
		},
		Detail: DetailRule{
			RecordSource:  "json_ld",
			Title:         req("json_ld", "$.title"),
			Description:   req("json_ld", "$.description"),
			IssuingEntity: req("json_ld", "$.hiringOrganization.name"),
			ApplyURL:      req("json_ld", "$.url"),
			AnchorCountry: req("json_ld", "$.jobLocation.address.addressCountry"),
		},
	}
}

func TestValidate_AcceptsValidRecipe(t *testing.T) {
	require.NoError(t, validRecipe().Validate())
}

func TestValidate_RejectsBadAcquisition(t *testing.T) {
	r := validRecipe()
	r.Acquisition = "telepathy"
	err := r.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "acquisition")
}

func TestValidate_RejectsMissingRequiredEnvelopeField(t *testing.T) {
	r := validRecipe()
	r.Detail.Title = FieldExtractor{} // no From, no Const
	err := r.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "title")
}

func TestValidate_RejectsUnknownTransform(t *testing.T) {
	r := validRecipe()
	r.Detail.Title.Transform = []string{"frobnicate"}
	err := r.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "frobnicate")
}

func TestValidate_RejectsUnparseableSelector(t *testing.T) {
	r := validRecipe()
	r.Detail.Title = FieldExtractor{From: []string{"selector"}, Selector: "a[unclosed"}
	err := r.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "selector")
}

func TestValidate_RejectsUnknownFromSource(t *testing.T) {
	r := validRecipe()
	r.Detail.Title = FieldExtractor{From: []string{"ouija"}, Const: "x"}
	err := r.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ouija")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/recipe/ -run TestValidate -v`
Expected: FAIL — `undefined: Recipe` (package/types don't exist yet).

- [ ] **Step 3: Write minimal implementation**

```go
// pkg/recipe/recipe.go
// Package recipe defines AI-generated, deterministic extraction recipes and
// the engine that runs them. This file holds the data types and structural
// validation. Nothing here calls an LLM or a database.
package recipe

import (
	"errors"
	"fmt"
	"time"

	"github.com/andybalholm/cascadia"
)

// validFromSources are the data planes a FieldExtractor may read from, in the
// order callers typically list them (structured data first, selectors last).
var validFromSources = map[string]bool{
	"json_ld": true, "next_data": true, "microdata": true,
	"selector": true, "meta": true, "record": true, "const": true,
}

// FieldExtractor describes how to pull ONE value from a page or record. From is
// tried in order; the first source that yields a non-empty value wins. The
// resolved value is then piped through Transform.
type FieldExtractor struct {
	From      []string `json:"from,omitempty"`
	JSONPath  string   `json:"json_path,omitempty"`  // json_ld / next_data / record
	Microdata string   `json:"microdata,omitempty"`  // schema.org itemprop
	Selector  string   `json:"selector,omitempty"`   // CSS selector
	Attr      string   `json:"attr,omitempty"`       // attribute to read (default: text)
	Meta      string   `json:"meta,omitempty"`       // <meta name|property>
	Const     string   `json:"const,omitempty"`      // literal fallback
	Transform []string `json:"transform,omitempty"`  // ordered transform names
	Required  bool     `json:"required,omitempty"`
}

// empty reports whether the extractor specifies no way to produce a value.
func (fx FieldExtractor) empty() bool {
	return len(fx.From) == 0 && fx.Const == ""
}

type KindRule struct {
	Mode  string         `json:"mode"` // "source_default" | "fixed" | "by_path"
	Fixed string         `json:"fixed,omitempty"`
	Path  FieldExtractor `json:"path,omitempty"`
}

type Pagination struct {
	Mode     string         `json:"mode"` // "none" | "page_param" | "cursor" | "next_link"
	Param    string         `json:"param,omitempty"`
	Cursor   FieldExtractor `json:"cursor,omitempty"`
	Next     FieldExtractor `json:"next,omitempty"`
	MaxPages int            `json:"max_pages,omitempty"`
}

type ListRule struct {
	Mode         string            `json:"mode"` // "api" | "sitemap" | "structured_data" | "selector"
	Endpoint     string            `json:"endpoint,omitempty"`
	Method       string            `json:"method,omitempty"`
	Params       map[string]string `json:"params,omitempty"`
	ItemsPath    string            `json:"items_path,omitempty"`
	ItemSelector string            `json:"item_selector,omitempty"`
	Link         FieldExtractor    `json:"link,omitempty"`
	Pagination   Pagination        `json:"pagination"`
}

type DetailRule struct {
	RecordSource string `json:"record_source"` // "api" | "json_ld" | "next_data" | "microdata" | "html"

	Title         FieldExtractor `json:"title"`
	Description   FieldExtractor `json:"description"`
	IssuingEntity FieldExtractor `json:"issuing_entity"`
	ApplyURL      FieldExtractor `json:"apply_url"`
	LocationText  FieldExtractor `json:"location_text,omitempty"`
	AnchorCountry FieldExtractor `json:"anchor_country"`
	Remote        FieldExtractor `json:"remote,omitempty"`
	PostedAt      FieldExtractor `json:"posted_at,omitempty"`
	Deadline      FieldExtractor `json:"deadline,omitempty"`
	AmountMin     FieldExtractor `json:"amount_min,omitempty"`
	AmountMax     FieldExtractor `json:"amount_max,omitempty"`
	Currency      FieldExtractor `json:"currency,omitempty"`
	Categories    FieldExtractor `json:"categories,omitempty"`

	CompanyLogoURL FieldExtractor `json:"company_logo_url,omitempty"`
	CompanyProfile FieldExtractor `json:"company_profile,omitempty"`

	Attributes map[string]FieldExtractor `json:"attributes,omitempty"`
}

type Recipe struct {
	Version      int       `json:"version"`
	GeneratedAt  time.Time `json:"generated_at"`
	Model        string    `json:"model,omitempty"`
	SampleURLs   []string  `json:"sample_urls,omitempty"`
	SampleHashes []string  `json:"sample_hashes,omitempty"`
	PassRate     float64   `json:"pass_rate,omitempty"`

	Acquisition string     `json:"acquisition"` // "api" | "structured_data" | "selectors"
	Kind        KindRule   `json:"kind"`
	List        ListRule   `json:"list"`
	Detail      DetailRule `json:"detail"`
}

var validAcquisition = map[string]bool{"api": true, "structured_data": true, "selectors": true}
var validListMode = map[string]bool{"api": true, "sitemap": true, "structured_data": true, "selector": true}
var validPaginationMode = map[string]bool{"none": true, "page_param": true, "cursor": true, "next_link": true}
var validKindMode = map[string]bool{"source_default": true, "fixed": true, "by_path": true}

// requiredEnvelopeFields are the universal fields opportunity.Verify() demands;
// every recipe must specify how to extract each.
func (d DetailRule) requiredEnvelopeFields() map[string]FieldExtractor {
	return map[string]FieldExtractor{
		"title":          d.Title,
		"description":    d.Description,
		"issuing_entity": d.IssuingEntity,
		"apply_url":      d.ApplyURL,
		"anchor_country": d.AnchorCountry,
	}
}

// Validate checks a recipe's structural integrity: enum fields, that every
// required envelope field has an extractor, and that every FieldExtractor uses
// known From sources, known transforms, and parseable selectors. It does NOT
// run the recipe. Returns a joined error describing every problem found.
func (r *Recipe) Validate() error {
	var errs []error

	if !validAcquisition[r.Acquisition] {
		errs = append(errs, fmt.Errorf("acquisition %q is not one of api/structured_data/selectors", r.Acquisition))
	}
	if !validListMode[r.List.Mode] {
		errs = append(errs, fmt.Errorf("list.mode %q is invalid", r.List.Mode))
	}
	if !validPaginationMode[r.List.Pagination.Mode] {
		errs = append(errs, fmt.Errorf("list.pagination.mode %q is invalid", r.List.Pagination.Mode))
	}
	if !validKindMode[r.Kind.Mode] {
		errs = append(errs, fmt.Errorf("kind.mode %q is invalid", r.Kind.Mode))
	}
	if r.Kind.Mode == "fixed" && r.Kind.Fixed == "" {
		errs = append(errs, errors.New("kind.mode=fixed requires kind.fixed"))
	}

	for name, fx := range r.Detail.requiredEnvelopeFields() {
		if fx.empty() {
			errs = append(errs, fmt.Errorf("detail.%s: required envelope field has no extractor", name))
		}
	}

	// Validate every extractor referenced anywhere in the recipe.
	check := func(label string, fx FieldExtractor) {
		if fx.empty() {
			return
		}
		for _, src := range fx.From {
			if !validFromSources[src] {
				errs = append(errs, fmt.Errorf("%s: unknown From source %q", label, src))
			}
		}
		for _, tn := range fx.Transform {
			if !transformExists(tn) {
				errs = append(errs, fmt.Errorf("%s: unknown transform %q", label, tn))
			}
		}
		if fx.Selector != "" {
			if _, err := cascadia.Compile(fx.Selector); err != nil {
				errs = append(errs, fmt.Errorf("%s: invalid selector %q: %w", label, fx.Selector, err))
			}
		}
	}

	check("list.link", r.List.Link)
	check("list.pagination.cursor", r.List.Pagination.Cursor)
	check("list.pagination.next", r.List.Pagination.Next)
	check("kind.path", r.Kind.Path)
	for name, fx := range r.Detail.requiredEnvelopeFields() {
		check("detail."+name, fx)
	}
	check("detail.location_text", r.Detail.LocationText)
	check("detail.remote", r.Detail.Remote)
	check("detail.posted_at", r.Detail.PostedAt)
	check("detail.deadline", r.Detail.Deadline)
	check("detail.amount_min", r.Detail.AmountMin)
	check("detail.amount_max", r.Detail.AmountMax)
	check("detail.currency", r.Detail.Currency)
	check("detail.categories", r.Detail.Categories)
	check("detail.company_logo_url", r.Detail.CompanyLogoURL)
	check("detail.company_profile", r.Detail.CompanyProfile)
	for k, fx := range r.Detail.Attributes {
		check("detail.attributes."+k, fx)
	}

	return errors.Join(errs...)
}
```

- [ ] **Step 4: Make `transformExists` resolvable for compilation**

`Validate()` references `transformExists`, defined in Task 2. To let Task 1 compile and test in isolation, add a temporary stub at the bottom of `recipe.go`; Task 2 deletes it.

```go
// TEMP (removed in Task 2 once transforms.go exists): allow recipe.go to
// compile before the transform registry lands. Tests in Task 1 use no
// transforms except "absolute_url".
func transformExists(name string) bool {
	return name == "absolute_url"
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./pkg/recipe/ -run TestValidate -v`
Expected: PASS (all six `TestValidate_*`).

- [ ] **Step 6: Tidy modules (promote cascadia to a direct dep)**

Run: `go mod tidy`
Expected: `github.com/andybalholm/cascadia` moves from indirect to direct in `go.mod`; no new downloads (already in the graph via goquery).

- [ ] **Step 7: Commit**

```bash
git add pkg/recipe/recipe.go pkg/recipe/recipe_test.go go.mod go.sum
git commit -m "feat(recipe): recipe types and structural Validate()"
```

---

## Task 2: Transform registry

**Files:**
- Create: `pkg/recipe/transforms.go`
- Modify: `pkg/recipe/recipe.go` (delete the temporary `transformExists` stub from Task 1, Step 4)
- Test: `pkg/recipe/transforms_test.go`

- [ ] **Step 1: Write the failing test**

```go
// pkg/recipe/transforms_test.go
package recipe

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyTransforms(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		names   []string
		base    string
		want    string
		wantErr bool
	}{
		{name: "trim", in: "  hi  ", names: []string{"trim"}, want: "hi"},
		{name: "lower", in: "HeLLo", names: []string{"lower"}, want: "hello"},
		{name: "collapse_ws", in: "a   b\n\tc", names: []string{"collapse_ws"}, want: "a b c"},
		{name: "html_to_text", in: "<p>Hi <b>there</b></p>", names: []string{"html_to_text"}, want: "Hi there"},
		{name: "chain trim+lower", in: "  YO ", names: []string{"trim", "lower"}, want: "yo"},
		{name: "absolute_url relative", in: "/jobs/1", names: []string{"absolute_url"}, base: "https://x.io/list", want: "https://x.io/jobs/1"},
		{name: "absolute_url already-absolute", in: "https://y.io/a", names: []string{"absolute_url"}, base: "https://x.io/list", want: "https://y.io/a"},
		{name: "parse_money", in: "USD 1,250.50", names: []string{"parse_money"}, want: "1250.50"},
		{name: "parse_date iso", in: "2026-06-09T10:00:00Z", names: []string{"parse_date"}, want: "2026-06-09T10:00:00Z"},
		{name: "parse_date ymd", in: "2026-06-09", names: []string{"parse_date"}, want: "2026-06-09T00:00:00Z"},
		{name: "unknown transform errors", in: "x", names: []string{"nope"}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := applyTransforms(tc.in, tc.names, transformCtx{BaseURL: tc.base})
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestTransformExists(t *testing.T) {
	assert.True(t, transformExists("trim"))
	assert.False(t, transformExists("frobnicate"))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/recipe/ -run 'TestApplyTransforms|TestTransformExists' -v`
Expected: FAIL — `undefined: applyTransforms` / `undefined: transformCtx`.

- [ ] **Step 3: Delete the temporary stub from Task 1**

In `pkg/recipe/recipe.go`, remove the entire `// TEMP ...` block and the `transformExists` function added in Task 1 Step 4 (the real one now lives in `transforms.go`).

- [ ] **Step 4: Write minimal implementation**

```go
// pkg/recipe/transforms.go
package recipe

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// transformCtx carries side data some transforms need (e.g. the page URL for
// resolving relative links). Most transforms ignore it.
type transformCtx struct {
	BaseURL string
}

type transformFn func(string, transformCtx) (string, error)

var wsRe = regexp.MustCompile(`\s+`)
var moneyStripRe = regexp.MustCompile(`[^0-9.\-]`)

// dateLayouts are tried in order by parse_date. Output is always RFC3339 (UTC).
var dateLayouts = []string{
	time.RFC3339, "2006-01-02T15:04:05", "2006-01-02",
	"02/01/2006", "01/02/2006", "January 2, 2006", "Jan 2, 2006", "2 January 2006",
}

var transformRegistry = map[string]transformFn{
	"trim":  func(s string, _ transformCtx) (string, error) { return strings.TrimSpace(s), nil },
	"lower": func(s string, _ transformCtx) (string, error) { return strings.ToLower(s), nil },
	"collapse_ws": func(s string, _ transformCtx) (string, error) {
		return strings.TrimSpace(wsRe.ReplaceAllString(s, " ")), nil
	},
	"html_to_text": func(s string, _ transformCtx) (string, error) {
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(s))
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(wsRe.ReplaceAllString(doc.Text(), " ")), nil
	},
	"absolute_url": func(s string, tc transformCtx) (string, error) {
		if s == "" || tc.BaseURL == "" {
			return s, nil
		}
		base, err := url.Parse(tc.BaseURL)
		if err != nil {
			return s, nil
		}
		ref, err := url.Parse(strings.TrimSpace(s))
		if err != nil {
			return s, nil
		}
		return base.ResolveReference(ref).String(), nil
	},
	"parse_money": func(s string, _ transformCtx) (string, error) {
		return moneyStripRe.ReplaceAllString(s, ""), nil
	},
	"parse_date": func(s string, _ transformCtx) (string, error) {
		s = strings.TrimSpace(s)
		for _, layout := range dateLayouts {
			if t, err := time.Parse(layout, s); err == nil {
				return t.UTC().Format(time.RFC3339), nil
			}
		}
		return "", fmt.Errorf("parse_date: unrecognized date %q", s)
	},
}

func transformExists(name string) bool {
	_, ok := transformRegistry[name]
	return ok
}

// applyTransforms pipes value through the named transforms in order.
func applyTransforms(value string, names []string, tc transformCtx) (string, error) {
	for _, name := range names {
		fn, ok := transformRegistry[name]
		if !ok {
			return "", fmt.Errorf("unknown transform %q", name)
		}
		out, err := fn(value, tc)
		if err != nil {
			return "", fmt.Errorf("transform %q: %w", name, err)
		}
		value = out
	}
	return value, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./pkg/recipe/ -run 'TestApplyTransforms|TestTransformExists|TestValidate' -v`
Expected: PASS (transform tests + the Task 1 Validate tests still green after removing the stub).

- [ ] **Step 6: Commit**

```bash
git add pkg/recipe/transforms.go pkg/recipe/transforms_test.go pkg/recipe/recipe.go
git commit -m "feat(recipe): transform registry (trim/lower/collapse_ws/html_to_text/absolute_url/parse_money/parse_date)"
```

---

## Task 3: `PageContext` + harvester

**Files:**
- Create: `pkg/recipe/pagecontext.go`
- Test: `pkg/recipe/pagecontext_test.go`

- [ ] **Step 1: Write the failing test**

```go
// pkg/recipe/pagecontext_test.go
package recipe

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const samplePage = `<!DOCTYPE html><html><head>
<meta property="og:image" content="https://cdn.x.io/logo.png">
<meta name="description" content="Great job">
<script type="application/ld+json">
{"@type":"JobPosting","title":"Senior Go Engineer","hiringOrganization":{"name":"ACME","logo":"https://cdn.x.io/acme.png"}}
</script>
<script id="__NEXT_DATA__" type="application/json">
{"props":{"pageProps":{"job":{"slug":"senior-go"}}}}
</script>
</head><body>
<h1 itemprop="title">Senior Go Engineer</h1>
<a class="apply" href="/apply/1">Apply</a>
</body></html>`

func TestNewPageContext_HarvestsAllPlanes(t *testing.T) {
	pc, err := NewPageContext("https://x.io/jobs/senior-go", samplePage, nil)
	require.NoError(t, err)

	// meta
	assert.Equal(t, "https://cdn.x.io/logo.png", pc.Meta["og:image"])
	assert.Equal(t, "Great job", pc.Meta["description"])

	// json-ld
	require.Len(t, pc.JSONLD, 1)
	assert.Equal(t, "Senior Go Engineer", pc.JSONLD[0]["title"])

	// __NEXT_DATA__
	require.NotNil(t, pc.NextData)
	props, _ := pc.NextData["props"].(map[string]any)
	require.NotNil(t, props)

	// DOM available
	require.NotNil(t, pc.HTML)
	assert.Equal(t, "Senior Go Engineer", pc.HTML.Find("h1[itemprop=title]").Text())

	// URL retained
	assert.Equal(t, "https://x.io/jobs/senior-go", pc.URL)
}

func TestNewPageContext_APIRecord(t *testing.T) {
	rec := map[string]any{"title": "From API"}
	pc, err := NewPageContext("https://x.io/api", "", rec)
	require.NoError(t, err)
	assert.Equal(t, "From API", pc.Record["title"])
}

func TestNewPageContext_ToleratesMalformedJSONLD(t *testing.T) {
	html := `<html><head><script type="application/ld+json">{not json}</script></head><body></body></html>`
	pc, err := NewPageContext("https://x.io", html, nil)
	require.NoError(t, err)
	assert.Empty(t, pc.JSONLD) // malformed block skipped, not fatal
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/recipe/ -run TestNewPageContext -v`
Expected: FAIL — `undefined: NewPageContext` / `undefined: PageContext`.

- [ ] **Step 3: Write minimal implementation**

```go
// pkg/recipe/pagecontext.go
package recipe

import (
	"encoding/json"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// PageContext normalizes the four data planes a page exposes so a
// FieldExtractor can try them in order. Record is set only in api mode.
type PageContext struct {
	URL      string
	HTML     *goquery.Document
	JSONLD   []map[string]any
	NextData map[string]any
	Meta     map[string]string
	Record   map[string]any
}

// NewPageContext parses html (may be empty for api mode) and harvests JSON-LD,
// __NEXT_DATA__-style state blobs, and meta tags. record is the API record in
// api mode (nil otherwise). Malformed embedded JSON is skipped, never fatal.
func NewPageContext(pageURL, html string, record map[string]any) (*PageContext, error) {
	pc := &PageContext{
		URL:    pageURL,
		Meta:   map[string]string{},
		Record: record,
	}

	if strings.TrimSpace(html) == "" {
		return pc, nil
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}
	pc.HTML = doc

	// meta: index by name OR property.
	doc.Find("meta").Each(func(_ int, s *goquery.Selection) {
		content, ok := s.Attr("content")
		if !ok {
			return
		}
		if name, ok := s.Attr("name"); ok {
			pc.Meta[name] = content
		}
		if prop, ok := s.Attr("property"); ok {
			pc.Meta[prop] = content
		}
	})

	// JSON-LD: every <script type="application/ld+json">. Objects are kept;
	// arrays and @graph wrappers are flattened to their object members.
	doc.Find(`script[type="application/ld+json"]`).Each(func(_ int, s *goquery.Selection) {
		for _, obj := range parseJSONLDBlock(s.Text()) {
			pc.JSONLD = append(pc.JSONLD, obj)
		}
	})

	// __NEXT_DATA__ / __NUXT__-style state blob (first parseable wins).
	doc.Find(`script[type="application/json"]`).EachWithBreak(func(_ int, s *goquery.Selection) bool {
		var m map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(s.Text())), &m); err == nil && len(m) > 0 {
			pc.NextData = m
			return false // stop
		}
		return true
	})

	return pc, nil
}

// parseJSONLDBlock parses one <script> body into zero or more JSON-LD objects,
// flattening top-level arrays and a top-level "@graph" array.
func parseJSONLDBlock(body string) []map[string]any {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}
	var raw any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil
	}
	return flattenJSONLD(raw)
}

func flattenJSONLD(raw any) []map[string]any {
	var out []map[string]any
	switch v := raw.(type) {
	case map[string]any:
		if g, ok := v["@graph"].([]any); ok {
			for _, item := range g {
				out = append(out, flattenJSONLD(item)...)
			}
		}
		out = append(out, v)
	case []any:
		for _, item := range v {
			out = append(out, flattenJSONLD(item)...)
		}
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/recipe/ -run TestNewPageContext -v`
Expected: PASS (all three).

- [ ] **Step 5: Commit**

```bash
git add pkg/recipe/pagecontext.go pkg/recipe/pagecontext_test.go
git commit -m "feat(recipe): PageContext harvester (json-ld/@graph, __NEXT_DATA__, meta, dom)"
```

---

## Task 4: `FieldExtractor` evaluation engine

**Files:**
- Create: `pkg/recipe/fieldeval.go`
- Test: `pkg/recipe/fieldeval_test.go`

- [ ] **Step 1: Write the failing test**

```go
// pkg/recipe/fieldeval_test.go
package recipe

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ctx(t *testing.T) *PageContext {
	t.Helper()
	pc, err := NewPageContext("https://x.io/jobs/senior-go", samplePage, nil)
	require.NoError(t, err)
	return pc
}

func TestEvaluate_JSONLDPath(t *testing.T) {
	fx := FieldExtractor{From: []string{"json_ld"}, JSONPath: "$.title"}
	got, err := Evaluate(fx, ctx(t))
	require.NoError(t, err)
	assert.Equal(t, "Senior Go Engineer", got)
}

func TestEvaluate_NestedJSONLDPath(t *testing.T) {
	fx := FieldExtractor{From: []string{"json_ld"}, JSONPath: "$.hiringOrganization.logo"}
	got, err := Evaluate(fx, ctx(t))
	require.NoError(t, err)
	assert.Equal(t, "https://cdn.x.io/acme.png", got)
}

func TestEvaluate_FromOrderingFirstNonEmptyWins(t *testing.T) {
	// json_ld has no "missing" key → fall through to selector (the <h1>).
	fx := FieldExtractor{
		From:     []string{"json_ld", "selector"},
		JSONPath: "$.missing",
		Selector: "h1[itemprop=title]",
	}
	got, err := Evaluate(fx, ctx(t))
	require.NoError(t, err)
	assert.Equal(t, "Senior Go Engineer", got)
}

func TestEvaluate_SelectorAttrWithTransform(t *testing.T) {
	fx := FieldExtractor{From: []string{"selector"}, Selector: "a.apply", Attr: "href", Transform: []string{"absolute_url"}}
	got, err := Evaluate(fx, ctx(t))
	require.NoError(t, err)
	assert.Equal(t, "https://x.io/apply/1", got)
}

func TestEvaluate_Meta(t *testing.T) {
	fx := FieldExtractor{From: []string{"meta"}, Meta: "og:image"}
	got, err := Evaluate(fx, ctx(t))
	require.NoError(t, err)
	assert.Equal(t, "https://cdn.x.io/logo.png", got)
}

func TestEvaluate_Const(t *testing.T) {
	fx := FieldExtractor{From: []string{"const"}, Const: "job"}
	got, err := Evaluate(fx, ctx(t))
	require.NoError(t, err)
	assert.Equal(t, "job", got)
}

func TestEvaluate_Record(t *testing.T) {
	pc, err := NewPageContext("https://x.io/api", "", map[string]any{"title": "API Title"})
	require.NoError(t, err)
	fx := FieldExtractor{From: []string{"record"}, JSONPath: "$.title"}
	got, err := Evaluate(fx, pc)
	require.NoError(t, err)
	assert.Equal(t, "API Title", got)
}

func TestEvaluate_EmptyWhenNothingResolves(t *testing.T) {
	fx := FieldExtractor{From: []string{"selector"}, Selector: ".nope"}
	got, err := Evaluate(fx, ctx(t))
	require.NoError(t, err)
	assert.Equal(t, "", got)
}

func TestEvaluateList_Selector(t *testing.T) {
	html := `<html><body><ul><li class="tag">Go</li><li class="tag">Remote</li></ul></body></html>`
	pc, err := NewPageContext("https://x.io", html, nil)
	require.NoError(t, err)
	fx := FieldExtractor{From: []string{"selector"}, Selector: "li.tag"}
	got, err := EvaluateList(fx, pc)
	require.NoError(t, err)
	assert.Equal(t, []string{"Go", "Remote"}, got)
}

func TestEvaluateList_JSONLDArray(t *testing.T) {
	html := `<html><head><script type="application/ld+json">{"skills":["Go","SQL"]}</script></head><body></body></html>`
	pc, err := NewPageContext("https://x.io", html, nil)
	require.NoError(t, err)
	fx := FieldExtractor{From: []string{"json_ld"}, JSONPath: "$.skills"}
	got, err := EvaluateList(fx, pc)
	require.NoError(t, err)
	assert.Equal(t, []string{"Go", "SQL"}, got)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/recipe/ -run 'TestEvaluate' -v`
Expected: FAIL — `undefined: Evaluate` / `undefined: EvaluateList`.

- [ ] **Step 3: Write minimal implementation**

```go
// pkg/recipe/fieldeval.go
package recipe

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/PaesslerAG/jsonpath"
	"github.com/PuerkitoBio/goquery"
)

// Evaluate resolves a single string value from a FieldExtractor against a
// PageContext. It walks fx.From in order, returning the first source that
// yields a non-empty value after fx.Transform is applied. Returns "" (no error)
// when nothing resolves — required-ness is enforced later by opportunity.Verify.
func Evaluate(fx FieldExtractor, pc *PageContext) (string, error) {
	for _, src := range fx.From {
		raw, err := resolveRaw(src, fx, pc)
		if err != nil {
			return "", err
		}
		if raw == "" {
			continue
		}
		out, err := applyTransforms(raw, fx.Transform, transformCtx{BaseURL: pc.URL})
		if err != nil {
			return "", err
		}
		if out != "" {
			return out, nil
		}
	}
	return "", nil
}

// EvaluateList resolves a multi-valued field (e.g. categories/skills). For
// selector sources it returns the text of every match; for json_ld/next_data/
// record it expects the JSONPath to resolve to an array.
func EvaluateList(fx FieldExtractor, pc *PageContext) ([]string, error) {
	for _, src := range fx.From {
		switch src {
		case "selector":
			if pc.HTML == nil || fx.Selector == "" {
				continue
			}
			var out []string
			pc.HTML.Find(fx.Selector).Each(func(_ int, s *goquery.Selection) {
				if v := strings.TrimSpace(s.Text()); v != "" {
					out = append(out, v)
				}
			})
			if len(out) > 0 {
				return out, nil
			}
		case "json_ld":
			for _, blob := range pc.JSONLD {
				if list := jsonPathList(fx.JSONPath, blob); len(list) > 0 {
					return list, nil
				}
			}
		case "next_data":
			if list := jsonPathList(fx.JSONPath, pc.NextData); len(list) > 0 {
				return list, nil
			}
		case "record":
			if list := jsonPathList(fx.JSONPath, pc.Record); len(list) > 0 {
				return list, nil
			}
		}
	}
	return nil, nil
}

// resolveRaw returns the untransformed value for one From source.
func resolveRaw(src string, fx FieldExtractor, pc *PageContext) (string, error) {
	switch src {
	case "const":
		return fx.Const, nil
	case "meta":
		return pc.Meta[fx.Meta], nil
	case "selector":
		if pc.HTML == nil || fx.Selector == "" {
			return "", nil
		}
		sel := pc.HTML.Find(fx.Selector).First()
		if sel.Length() == 0 {
			return "", nil
		}
		if fx.Attr != "" {
			v, _ := sel.Attr(fx.Attr)
			return strings.TrimSpace(v), nil
		}
		return strings.TrimSpace(sel.Text()), nil
	case "microdata":
		if pc.HTML == nil || fx.Microdata == "" {
			return "", nil
		}
		sel := pc.HTML.Find(fmt.Sprintf("[itemprop=%q]", fx.Microdata)).First()
		if sel.Length() == 0 {
			return "", nil
		}
		if v, ok := sel.Attr("content"); ok {
			return strings.TrimSpace(v), nil
		}
		return strings.TrimSpace(sel.Text()), nil
	case "json_ld":
		for _, blob := range pc.JSONLD {
			if v := jsonPathScalar(fx.JSONPath, blob); v != "" {
				return v, nil
			}
		}
		return "", nil
	case "next_data":
		return jsonPathScalar(fx.JSONPath, pc.NextData), nil
	case "record":
		return jsonPathScalar(fx.JSONPath, pc.Record), nil
	default:
		return "", fmt.Errorf("unknown From source %q", src)
	}
}

// jsonPathScalar evaluates path against root and renders a scalar result. A
// missing path or non-scalar result yields "".
func jsonPathScalar(path string, root any) string {
	if path == "" || root == nil {
		return ""
	}
	v, err := jsonpath.Get(path, root)
	if err != nil || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return ""
	}
}

func jsonPathList(path string, root any) []string {
	if path == "" || root == nil {
		return nil
	}
	v, err := jsonpath.Get(path, root)
	if err != nil {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, item := range arr {
		if s, ok := item.(string); ok {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/recipe/ -run 'TestEvaluate' -v`
Expected: PASS (all Evaluate + EvaluateList cases).

- [ ] **Step 5: Run the full package with race + vet**

Run: `go test -race ./pkg/recipe/... && go vet ./pkg/recipe/...`
Expected: PASS, no vet warnings.

- [ ] **Step 6: Commit**

```bash
git add pkg/recipe/fieldeval.go pkg/recipe/fieldeval_test.go
git commit -m "feat(recipe): FieldExtractor evaluation engine (json-ld/next_data/microdata/selector/meta/record + lists)"
```

---

## Self-Review

**Spec coverage (this plan = §4 + §5A only):**
- §4 recipe types (Recipe, FieldExtractor, ListRule, Pagination, DetailRule, KindRule) → Task 1. ✓
- §4 "structured-data-first, selector fallback" via ordered `From` → Task 4 (`Evaluate` ordering test). ✓
- §5A PageContext four planes (HTML/JSON-LD/`__NEXT_DATA__`/meta) + shared harvester → Task 3. ✓
- §5A transform registry (fixed allowlist, pure funcs) → Task 2. ✓
- §6 safety "unknown transform rejected at Validate()" → Task 1 (`TestValidate_RejectsUnknownTransform`). ✓
- §11 JSON-path dialect resolved: `github.com/PaesslerAG/jsonpath` (already a dep, used in `pkg/connectors/spec/jsonfeed`). ✓
- Deferred to later plans (correctly out of scope here): Executor/drivers (Plan 2), Store/migrations (Plan 3), Generator/Validator/handlers/config (Plan 4), rollout (Plan 5).

**Placeholder scan:** none — every step has complete code and exact run commands. The one temporary stub (Task 1 Step 4) is explicitly created and explicitly removed in Task 2 Step 3.

**Type consistency:** `transformExists`/`applyTransforms`/`transformCtx` defined in Task 2 and referenced by Task 1 (`Validate`) and Task 4 (`Evaluate`) — signatures match. `PageContext` fields used by `Evaluate`/`EvaluateList` match Task 3's definition (`HTML`, `JSONLD`, `NextData`, `Meta`, `Record`, `URL`). `FieldExtractor` field names (`From`, `JSONPath`, `Selector`, `Attr`, `Meta`, `Const`, `Microdata`, `Transform`, `Required`) are identical across Tasks 1 and 4.
