# Extraction Recipes — Plan 2: Deterministic Executor

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the deterministic `Executor` that turns a `recipe.Recipe` + a `domain.Source` into `[]domain.ExternalOpportunity` — replacing the universal connector's per-page LLM `DiscoverLinks()` + `Extract()` — plus a `connectors.CrawlIterator` adapter so the existing crawl pipeline can consume it unchanged.

**Architecture:** A `Fetcher` interface abstracts HTTP (testable with `httptest`). `buildOpportunity` maps a `PageContext` (Plan 1) through the recipe's `DetailRule` into an `ExternalOpportunity` (kind-agnostic; company name/logo/profile folded in). Two acquisition drivers — `api` (records straight from a JSON endpoint) and `html` (enumerate detail URLs from a listing, fetch each) — feed a paginating walk. A thin adapter in `pkg/connectors/recipeconn` exposes it as a `connectors.CrawlIterator`. No LLM, no DB, no Frame.

**Tech Stack:** Go 1.26, `github.com/PuerkitoBio/goquery`, `github.com/PaesslerAG/jsonpath`, `pkg/recipe` (Plan 1), `pkg/domain`, `pkg/connectors`, `pkg/content`, `github.com/stretchr/testify`, `net/http/httptest`.

**Spec:** `docs/superpowers/specs/2026-06-09-ai-generated-extraction-recipes-design.md` (§5B Executor).
**Depends on:** Plan 1 (`pkg/recipe`: `Recipe`, `FieldExtractor`, `PageContext`/`NewPageContext`, `Evaluate`/`EvaluateList`), now merged.

**Sequencing note — deliberately NOT in this plan:** the live swap in `apps/crawler/service/crawl_request_handler.go:222` (use the Executor when a source has a recipe) requires the `Source.extraction_recipe` field, which lands in **Plan 3 (Store + migrations)**. Plan 2 delivers the Executor and the `CrawlIterator` adapter as a complete, tested unit constructed from a `Recipe` passed in; Plan 3 wires it into source resolution. This keeps Plan 2 shippable without a schema change.

---

## File Structure

| File | Responsibility |
|------|----------------|
| `pkg/recipe/fetcher.go` | `Fetcher` interface (HTTP GET seam) |
| `pkg/recipe/build.go` | `buildOpportunity` + field/kind/parse helpers (PageContext → ExternalOpportunity) |
| `pkg/recipe/executor.go` | `Executor`: `apiPage` / `htmlPage` drivers + `Crawl` paginating walk + SSRF host guard |
| `pkg/connectors/recipeconn/recipeconn.go` | `connectors.CrawlIterator` adapter over `Executor` |
| `pkg/recipe/*_test.go`, `pkg/connectors/recipeconn/*_test.go` | tests (fixtures + `httptest`) |

---

## Task 1: `Fetcher` + `buildOpportunity`

**Files:**
- Create: `pkg/recipe/fetcher.go`, `pkg/recipe/build.go`
- Test: `pkg/recipe/build_test.go`

- [ ] **Step 1: Write the failing test**

```go
// pkg/recipe/build_test.go
package recipe

import (
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const detailPage = `<!DOCTYPE html><html><head>
<meta property="og:image" content="https://cdn.x.io/logo.png">
<script type="application/ld+json">
{"@type":"JobPosting","title":"Senior Go Engineer","description":"We need a strong Go engineer to build distributed systems and own delivery end to end.",
"hiringOrganization":{"name":"ACME","logo":"https://cdn.x.io/acme.png","description":"ACME builds things."},
"url":"https://x.io/jobs/senior-go","datePosted":"2026-06-01","jobLocation":{"address":{"addressCountry":"KE"}},
"baseSalary":{"value":{"minValue":1000,"maxValue":2000,"currency":"USD"}}}
</script></head><body><h1 itemprop="title">Senior Go Engineer</h1></body></html>`

// jobDetailRule is a representative structured-data recipe for a job.
func jobDetailRule() DetailRule {
	jl := func(path string) FieldExtractor { return FieldExtractor{From: []string{"json_ld"}, JSONPath: path} }
	return DetailRule{
		RecordSource:   "json_ld",
		Title:          jl("$.title"),
		Description:    jl("$.description"),
		IssuingEntity:  jl("$.hiringOrganization.name"),
		ApplyURL:       jl("$.url"),
		AnchorCountry:  jl("$.jobLocation.address.addressCountry"),
		PostedAt:       FieldExtractor{From: []string{"json_ld"}, JSONPath: "$.datePosted", Transform: []string{"parse_date"}},
		AmountMin:      jl("$.baseSalary.value.minValue"),
		AmountMax:      jl("$.baseSalary.value.maxValue"),
		Currency:       jl("$.baseSalary.value.currency"),
		CompanyLogoURL: FieldExtractor{From: []string{"json_ld", "meta"}, JSONPath: "$.hiringOrganization.logo", Meta: "og:image"},
		CompanyProfile: jl("$.hiringOrganization.description"),
	}
}

func jobSource() domain.Source {
	s := domain.Source{Type: "brightermonday", BaseURL: "https://x.io", Country: "NG", Kinds: []string{"job"}}
	s.ID = "src_1"
	return s
}

func TestBuildOpportunity_MapsAllFields(t *testing.T) {
	pc, err := NewPageContext("https://x.io/jobs/senior-go", detailPage, nil)
	require.NoError(t, err)
	r := &Recipe{Acquisition: "structured_data", Kind: KindRule{Mode: "source_default"}, Detail: jobDetailRule()}

	opp, err := buildOpportunity(pc, jobSource(), r)
	require.NoError(t, err)

	assert.Equal(t, "job", opp.Kind)
	assert.Equal(t, "Senior Go Engineer", opp.Title)
	assert.Contains(t, opp.Description, "Go engineer")
	assert.Equal(t, "ACME", opp.IssuingEntity)
	assert.Equal(t, "https://x.io/jobs/senior-go", opp.ApplyURL)
	assert.Equal(t, "https://x.io/jobs/senior-go", opp.ExternalID) // derived from apply URL
	assert.Equal(t, "src_1", opp.SourceID)
	assert.EqualValues(t, "brightermonday", opp.Source)
	require.NotNil(t, opp.AnchorLocation)
	assert.Equal(t, "KE", opp.AnchorLocation.Country)
	require.NotNil(t, opp.PostedAt)
	assert.Equal(t, 2026, opp.PostedAt.Year())
	assert.Equal(t, 1000.0, opp.AmountMin)
	assert.Equal(t, 2000.0, opp.AmountMax)
	assert.Equal(t, "USD", opp.Currency)
	assert.Equal(t, "https://cdn.x.io/acme.png", opp.Attributes["company_logo_url"])
	assert.Equal(t, "ACME builds things.", opp.Attributes["company_profile"])
}

func TestBuildOpportunity_AnchorCountryFallsBackToSource(t *testing.T) {
	// Detail page with no country in the structured data.
	html := `<html><head><script type="application/ld+json">{"title":"x","description":"` +
		`a description long enough to clear the fifty character minimum gate.","url":"https://x.io/j/1"}</script></head><body></body></html>`
	pc, err := NewPageContext("https://x.io/j/1", html, nil)
	require.NoError(t, err)
	r := &Recipe{Acquisition: "structured_data", Kind: KindRule{Mode: "source_default"}, Detail: jobDetailRule()}

	opp, err := buildOpportunity(pc, jobSource(), r) // source.Country = "NG"
	require.NoError(t, err)
	require.NotNil(t, opp.AnchorLocation)
	assert.Equal(t, "NG", opp.AnchorLocation.Country)
}

func TestBuildOpportunity_KindFixedAndByPath(t *testing.T) {
	pc, err := NewPageContext("https://x.io/s/1",
		`<html><head><script type="application/ld+json">{"kind":"scholarship"}</script></head><body></body></html>`, nil)
	require.NoError(t, err)

	rFixed := &Recipe{Kind: KindRule{Mode: "fixed", Fixed: "tender"}, Detail: DetailRule{}}
	opp, err := buildOpportunity(pc, jobSource(), rFixed)
	require.NoError(t, err)
	assert.Equal(t, "tender", opp.Kind)

	rPath := &Recipe{Kind: KindRule{Mode: "by_path", Path: FieldExtractor{From: []string{"json_ld"}, JSONPath: "$.kind"}}, Detail: DetailRule{}}
	opp, err = buildOpportunity(pc, jobSource(), rPath)
	require.NoError(t, err)
	assert.Equal(t, "scholarship", opp.Kind)
}

func TestBuildOpportunity_SourceDefaultMultiKindErrors(t *testing.T) {
	pc, _ := NewPageContext("https://x.io", "<html><body></body></html>", nil)
	src := jobSource()
	src.Kinds = []string{"job", "scholarship"}
	r := &Recipe{Kind: KindRule{Mode: "source_default"}, Detail: DetailRule{}}
	_, err := buildOpportunity(pc, src, r)
	require.Error(t, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/recipe/ -run TestBuildOpportunity -v`
Expected: FAIL — `undefined: buildOpportunity`.

- [ ] **Step 3: Write `pkg/recipe/fetcher.go`**

```go
package recipe

import "context"

// Fetcher abstracts an HTTP GET so the Executor can be driven by a real client
// in production and by an httptest server in tests. The production adapter
// (Plan 3) wraps the crawler's httpx.Client.
type Fetcher interface {
	Get(ctx context.Context, url string) (body []byte, status int, err error)
}
```

- [ ] **Step 4: Write `pkg/recipe/build.go`**

```go
package recipe

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// buildOpportunity maps a PageContext through the recipe's DetailRule into a
// domain.ExternalOpportunity. It is kind-agnostic: every kind-specific field
// flows through DetailRule.Attributes. Transform errors on a field are treated
// as "field absent" — the downstream opportunity.Verify() gate decides whether
// a missing required field is fatal.
func buildOpportunity(pc *PageContext, src domain.Source, r *Recipe) (domain.ExternalOpportunity, error) {
	kind, err := resolveKind(r, pc, src)
	if err != nil {
		return domain.ExternalOpportunity{}, err
	}

	val := func(fx FieldExtractor) string {
		if fx.empty() {
			return ""
		}
		v, _ := Evaluate(fx, pc) // transform errors -> "" (treated as absent)
		return v
	}

	d := r.Detail
	opp := domain.ExternalOpportunity{
		Kind:          kind,
		SourceID:      src.ID,
		Source:        src.Type,
		SourceURL:     pc.URL,
		Title:         val(d.Title),
		Description:   val(d.Description),
		IssuingEntity: val(d.IssuingEntity),
		ApplyURL:      val(d.ApplyURL),
		LocationText:  val(d.LocationText),
		Remote:        parseBool(val(d.Remote)),
		PostedAt:      parseTime(val(d.PostedAt)),
		Deadline:      parseTime(val(d.Deadline)),
		AmountMin:     parseFloat(val(d.AmountMin)),
		AmountMax:     parseFloat(val(d.AmountMax)),
		Currency:      val(d.Currency),
	}

	// ExternalID (dedup key) derives from the apply URL.
	opp.ExternalID = opp.ApplyURL

	// Anchor country: extracted value, else the source's declared country.
	country := val(d.AnchorCountry)
	if country == "" {
		country = src.Country
	}
	if country != "" {
		opp.AnchorLocation = &domain.Location{Country: country}
	}

	// Categories (list-valued).
	if !d.Categories.empty() {
		if cats, _ := EvaluateList(d.Categories, pc); len(cats) > 0 {
			opp.Categories = cats
		}
	}

	// Kind-specific attributes + folded-in company logo/profile.
	attrs := map[string]any{}
	for key, fx := range d.Attributes {
		if v := val(fx); v != "" {
			attrs[key] = v
		}
	}
	if v := val(d.CompanyLogoURL); v != "" {
		attrs["company_logo_url"] = v
	}
	if v := val(d.CompanyProfile); v != "" {
		attrs["company_profile"] = v
	}
	if len(attrs) > 0 {
		opp.Attributes = attrs
	}

	return opp, nil
}

// resolveKind determines the opportunity kind deterministically — never a
// per-page LLM classifier.
func resolveKind(r *Recipe, pc *PageContext, src domain.Source) (string, error) {
	switch r.Kind.Mode {
	case "fixed":
		return r.Kind.Fixed, nil
	case "by_path":
		v, _ := Evaluate(r.Kind.Path, pc)
		return v, nil
	default: // "source_default"
		switch len(src.Kinds) {
		case 1:
			return src.Kinds[0], nil
		case 0:
			return "", nil
		default:
			return "", fmt.Errorf("kind source_default needs exactly one source kind, source has %d; recipe must use fixed or by_path", len(src.Kinds))
		}
	}
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "remote", "1":
		return true
	}
	return false
}

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

// parseTime parses an RFC3339 timestamp (recipes normalize dates via the
// parse_date transform). Returns nil on empty or unparseable input.
func parseTime(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./pkg/recipe/ -run TestBuildOpportunity -v`
Expected: PASS (all four).

- [ ] **Step 6: Commit**

```bash
git add pkg/recipe/fetcher.go pkg/recipe/build.go pkg/recipe/build_test.go
git commit -m "feat(recipe): buildOpportunity maps PageContext -> ExternalOpportunity (kind-agnostic, company/logo folded in)"
```

---

## Task 2: api-mode page driver

**Files:**
- Create: `pkg/recipe/executor.go`
- Test: `pkg/recipe/executor_api_test.go`

- [ ] **Step 1: Write the failing test**

```go
// pkg/recipe/executor_api_test.go
package recipe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// httptestFetcher adapts an httptest server to the Fetcher interface.
type httptestFetcher struct{ client *http.Client }

func (f httptestFetcher) Get(ctx context.Context, url string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, rerr := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if rerr != nil {
			break
		}
	}
	return buf, resp.StatusCode, nil
}

func TestExecutor_APIPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jobs":[
			{"title":"Go Eng","description":"A description that is definitely longer than fifty characters for sure.","company":"ACME","apply":"https://x.io/j/1","country":"KE"},
			{"title":"Py Eng","description":"Another description that is also longer than fifty characters total.","company":"BETA","apply":"https://x.io/j/2","country":"NG"}
		]}`))
	}))
	defer srv.Close()

	rec := func(path string) FieldExtractor { return FieldExtractor{From: []string{"record"}, JSONPath: path} }
	r := &Recipe{
		Acquisition: "api",
		Kind:        KindRule{Mode: "source_default"},
		List:        ListRule{Mode: "api", Endpoint: "/api/jobs", ItemsPath: "$.jobs", Pagination: Pagination{Mode: "none"}},
		Detail: DetailRule{
			RecordSource:  "record",
			Title:         rec("$.title"),
			Description:   rec("$.description"),
			IssuingEntity: rec("$.company"),
			ApplyURL:      rec("$.apply"),
			AnchorCountry: rec("$.country"),
		},
	}
	e := NewExecutor(r, httptestFetcher{client: srv.Client()})
	src := jobSource()
	src.BaseURL = srv.URL

	items, raw, status, _, err := e.apiPage(context.Background(), src, srv.URL+"/api/jobs")
	require.NoError(t, err)
	assert.Equal(t, 200, status)
	assert.NotEmpty(t, raw)
	require.Len(t, items, 2)
	assert.Equal(t, "Go Eng", items[0].Title)
	assert.Equal(t, "ACME", items[0].IssuingEntity)
	assert.Equal(t, "KE", items[0].AnchorLocation.Country)
	assert.Equal(t, "Py Eng", items[1].Title)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/recipe/ -run TestExecutor_APIPage -v`
Expected: FAIL — `undefined: NewExecutor`.

- [ ] **Step 3: Write `pkg/recipe/executor.go`**

```go
package recipe

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/PaesslerAG/jsonpath"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// Executor runs a recipe deterministically — no LLM. It is constructed per
// source with that source's active recipe and a Fetcher for HTTP.
type Executor struct {
	recipe  *Recipe
	fetcher Fetcher
}

// NewExecutor builds an Executor for a recipe + fetcher.
func NewExecutor(r *Recipe, f Fetcher) *Executor {
	return &Executor{recipe: r, fetcher: f}
}

// apiPage fetches one api-mode page from pageURL, parses the records under
// List.ItemsPath, and builds an opportunity per record. It returns the page's
// items, the raw body, the HTTP status, the root JSON (for cursor pagination),
// and any error.
func (e *Executor) apiPage(ctx context.Context, src domain.Source, pageURL string) (items []domain.ExternalOpportunity, raw []byte, status int, root any, err error) {
	raw, status, err = e.fetcher.Get(ctx, pageURL)
	if err != nil {
		return nil, raw, status, nil, err
	}
	if status < 200 || status >= 300 {
		return nil, raw, status, nil, fmt.Errorf("api page %s returned status %d", pageURL, status)
	}
	if err = json.Unmarshal(raw, &root); err != nil {
		return nil, raw, status, nil, fmt.Errorf("api page %s: invalid JSON: %w", pageURL, err)
	}
	recsVal, err := jsonpath.Get(e.recipe.List.ItemsPath, root)
	if err != nil {
		return nil, raw, status, root, fmt.Errorf("api page %s: items_path %q: %w", pageURL, e.recipe.List.ItemsPath, err)
	}
	recs, ok := recsVal.([]any)
	if !ok {
		return nil, raw, status, root, nil // no array -> empty page
	}
	for _, rv := range recs {
		rec, ok := rv.(map[string]any)
		if !ok {
			continue
		}
		pc, perr := NewPageContext(pageURL, "", rec)
		if perr != nil {
			return nil, raw, status, root, perr
		}
		opp, berr := buildOpportunity(pc, src, e.recipe)
		if berr != nil {
			return nil, raw, status, root, berr
		}
		items = append(items, opp)
	}
	return items, raw, status, root, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/recipe/ -run TestExecutor_APIPage -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/recipe/executor.go pkg/recipe/executor_api_test.go
git commit -m "feat(recipe): Executor api-mode page driver (ItemsPath -> records -> opportunities)"
```

---

## Task 3: html-mode page driver

**Files:**
- Modify: `pkg/recipe/executor.go`
- Test: `pkg/recipe/executor_html_test.go`

- [ ] **Step 1: Write the failing test**

```go
// pkg/recipe/executor_html_test.go
package recipe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutor_HTMLPage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/jobs", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body>
			<div class="card"><a class="lnk" href="/jobs/1">Job 1</a></div>
			<div class="card"><a class="lnk" href="/jobs/2">Job 2</a></div>
		</body></html>`))
	})
	detail := func(title string) string {
		return `<html><head><script type="application/ld+json">{"title":"` + title +
			`","description":"A sufficiently long description that clears the fifty char minimum gate.",` +
			`"hiringOrganization":{"name":"ACME"},"url":"https://x.io/apply","jobLocation":{"address":{"addressCountry":"KE"}}}</script></head><body></body></html>`
	}
	mux.HandleFunc("/jobs/1", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(detail("Job One"))) })
	mux.HandleFunc("/jobs/2", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(detail("Job Two"))) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	jl := func(p string) FieldExtractor { return FieldExtractor{From: []string{"json_ld"}, JSONPath: p} }
	r := &Recipe{
		Acquisition: "selectors",
		Kind:        KindRule{Mode: "source_default"},
		List: ListRule{
			Mode:         "selector",
			ItemSelector: "div.card",
			Link:         FieldExtractor{From: []string{"selector"}, Selector: "a.lnk", Attr: "href", Transform: []string{"absolute_url"}},
			Pagination:   Pagination{Mode: "none"},
		},
		Detail: DetailRule{
			RecordSource:  "json_ld",
			Title:         jl("$.title"),
			Description:   jl("$.description"),
			IssuingEntity: jl("$.hiringOrganization.name"),
			ApplyURL:      jl("$.url"),
			AnchorCountry: jl("$.jobLocation.address.addressCountry"),
		},
	}
	e := NewExecutor(r, httptestFetcher{client: srv.Client()})
	src := jobSource()
	src.BaseURL = srv.URL

	items, raw, status, _, err := e.htmlPage(context.Background(), src, srv.URL+"/jobs")
	require.NoError(t, err)
	assert.Equal(t, 200, status)
	assert.NotEmpty(t, raw)
	require.Len(t, items, 2)
	assert.Equal(t, "Job One", items[0].Title)
	assert.Equal(t, "Job Two", items[1].Title)
	assert.Equal(t, "ACME", items[0].IssuingEntity)
}

func TestExecutor_HTMLPage_SkipsCrossHostLinks(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/jobs", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body>
			<div class="card"><a class="lnk" href="https://evil.example/x">Bad</a></div>
		</body></html>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	r := &Recipe{
		Acquisition: "selectors",
		Kind:        KindRule{Mode: "source_default"},
		List: ListRule{Mode: "selector", ItemSelector: "div.card",
			Link:       FieldExtractor{From: []string{"selector"}, Selector: "a.lnk", Attr: "href", Transform: []string{"absolute_url"}},
			Pagination: Pagination{Mode: "none"}},
		Detail: DetailRule{RecordSource: "json_ld", Title: FieldExtractor{From: []string{"json_ld"}, JSONPath: "$.title"},
			Description: FieldExtractor{From: []string{"json_ld"}, JSONPath: "$.description"},
			IssuingEntity: FieldExtractor{From: []string{"json_ld"}, JSONPath: "$.c"},
			ApplyURL: FieldExtractor{From: []string{"json_ld"}, JSONPath: "$.u"},
			AnchorCountry: FieldExtractor{From: []string{"json_ld"}, JSONPath: "$.k"}},
	}
	e := NewExecutor(r, httptestFetcher{client: srv.Client()})
	src := jobSource()
	src.BaseURL = srv.URL

	items, _, _, _, err := e.htmlPage(context.Background(), src, srv.URL+"/jobs")
	require.NoError(t, err)
	assert.Empty(t, items) // cross-host detail link skipped (SSRF guard)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/recipe/ -run TestExecutor_HTMLPage -v`
Expected: FAIL — `e.htmlPage undefined`.

- [ ] **Step 3: Add `htmlPage` + helpers to `pkg/recipe/executor.go`**

Add these imports to the existing import block in `executor.go`: `"net/url"`, `"strings"`, and `"github.com/PuerkitoBio/goquery"`. Then append:

```go
// htmlPage fetches one listing page, enumerates detail URLs via the recipe's
// ItemSelector + Link, fetches each same-host detail page, and builds an
// opportunity from it. Cross-host detail links are skipped (SSRF guard).
// It returns the items, the listing's raw body, its HTTP status, the listing
// PageContext (for next-link pagination), and any error.
func (e *Executor) htmlPage(ctx context.Context, src domain.Source, listURL string) (items []domain.ExternalOpportunity, raw []byte, status int, listPC *PageContext, err error) {
	raw, status, err = e.fetcher.Get(ctx, listURL)
	if err != nil {
		return nil, raw, status, nil, err
	}
	if status < 200 || status >= 300 {
		return nil, raw, status, nil, fmt.Errorf("listing %s returned status %d", listURL, status)
	}
	listPC, err = NewPageContext(listURL, string(raw), nil)
	if err != nil {
		return nil, raw, status, nil, err
	}

	detailURLs := e.collectDetailURLs(listPC, listURL)
	for _, du := range detailURLs {
		if !sameHost(src.BaseURL, du) {
			continue // SSRF guard: only follow links on the source's own host
		}
		body, st, ferr := e.fetcher.Get(ctx, du)
		if ferr != nil {
			return items, raw, status, listPC, ferr
		}
		if st < 200 || st >= 300 {
			continue // skip unreachable detail pages, keep crawling
		}
		pc, perr := NewPageContext(du, string(body), nil)
		if perr != nil {
			return items, raw, status, listPC, perr
		}
		opp, berr := buildOpportunity(pc, src, e.recipe)
		if berr != nil {
			return items, raw, status, listPC, berr
		}
		items = append(items, opp)
	}
	return items, raw, status, listPC, nil
}

// collectDetailURLs evaluates the recipe's Link extractor inside each
// ItemSelector match, returning resolved detail URLs.
func (e *Executor) collectDetailURLs(listPC *PageContext, listURL string) []string {
	if listPC.HTML == nil || e.recipe.List.ItemSelector == "" {
		return nil
	}
	var urls []string
	listPC.HTML.Find(e.recipe.List.ItemSelector).Each(func(_ int, item *goquery.Selection) {
		outer, oerr := goquery.OuterHtml(item)
		if oerr != nil {
			return
		}
		itemPC, perr := NewPageContext(listURL, outer, nil)
		if perr != nil {
			return
		}
		if link, _ := Evaluate(e.recipe.List.Link, itemPC); link != "" {
			urls = append(urls, link)
		}
	})
	return urls
}

// sameHost reports whether target shares base's host (SSRF guard). A target
// that fails to parse, or a base without a host, is treated as not-same-host.
func sameHost(base, target string) bool {
	b, err := url.Parse(base)
	if err != nil || b.Host == "" {
		return false
	}
	t, err := url.Parse(target)
	if err != nil {
		return false
	}
	return strings.EqualFold(b.Host, t.Host)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/recipe/ -run TestExecutor_HTMLPage -v`
Expected: PASS (both cases).

- [ ] **Step 5: Commit**

```bash
git add pkg/recipe/executor.go pkg/recipe/executor_html_test.go
git commit -m "feat(recipe): Executor html-mode driver (listing -> detail URLs -> per-page extract) with SSRF host guard"
```

---

## Task 4: Pagination + top-level `Crawl`

**Files:**
- Modify: `pkg/recipe/executor.go`
- Test: `pkg/recipe/executor_paginate_test.go`

- [ ] **Step 1: Write the failing test**

```go
// pkg/recipe/executor_paginate_test.go
package recipe

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutor_Crawl_APIPageParam(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		page := req.URL.Query().Get("page")
		w.Header().Set("Content-Type", "application/json")
		switch page {
		case "", "1":
			_, _ = w.Write([]byte(`{"jobs":[{"title":"A","description":"A description long enough to pass the fifty character minimum gate.","company":"X","apply":"https://x.io/a","country":"KE"}]}`))
		case "2":
			_, _ = w.Write([]byte(`{"jobs":[{"title":"B","description":"Another description that also clears the fifty character minimum gate.","company":"Y","apply":"https://x.io/b","country":"KE"}]}`))
		default:
			_, _ = w.Write([]byte(`{"jobs":[]}`)) // empty -> stop
		}
	}))
	defer srv.Close()

	rec := func(p string) FieldExtractor { return FieldExtractor{From: []string{"record"}, JSONPath: p} }
	r := &Recipe{
		Acquisition: "api",
		Kind:        KindRule{Mode: "source_default"},
		List: ListRule{Mode: "api", Endpoint: "/api/jobs", ItemsPath: "$.jobs",
			Pagination: Pagination{Mode: "page_param", Param: "page", MaxPages: 5}},
		Detail: DetailRule{RecordSource: "record", Title: rec("$.title"), Description: rec("$.description"),
			IssuingEntity: rec("$.company"), ApplyURL: rec("$.apply"), AnchorCountry: rec("$.country")},
	}
	src := jobSource()
	src.BaseURL = srv.URL
	e := NewExecutor(r, httptestFetcher{client: srv.Client()})

	all, err := drain(t, e, src)
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "A", all[0].Title)
	assert.Equal(t, "B", all[1].Title)
}

func TestExecutor_Crawl_HTMLNextLink(t *testing.T) {
	mux := http.NewServeMux()
	page := func(title, next string) string {
		nextLink := ""
		if next != "" {
			nextLink = fmt.Sprintf(`<a rel="next" href="%s">next</a>`, next)
		}
		return fmt.Sprintf(`<html><body><div class="card"><a class="lnk" href="/d/%s">x</a></div>%s</body></html>`, title, nextLink)
	}
	detail := `<html><head><script type="application/ld+json">{"title":"T","description":"A description long enough to clear the fifty character minimum requirement.","hiringOrganization":{"name":"ACME"},"url":"https://x.io/apply","jobLocation":{"address":{"addressCountry":"KE"}}}</script></head><body></body></html>`
	mux.HandleFunc("/p1", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(page("1", "/p2"))) })
	mux.HandleFunc("/p2", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(page("2", ""))) })
	mux.HandleFunc("/d/1", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(detail)) })
	mux.HandleFunc("/d/2", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(detail)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	jl := func(p string) FieldExtractor { return FieldExtractor{From: []string{"json_ld"}, JSONPath: p} }
	r := &Recipe{
		Acquisition: "selectors",
		Kind:        KindRule{Mode: "source_default"},
		List: ListRule{Mode: "selector", ItemSelector: "div.card",
			Link:       FieldExtractor{From: []string{"selector"}, Selector: "a.lnk", Attr: "href", Transform: []string{"absolute_url"}},
			Pagination: Pagination{Mode: "next_link", MaxPages: 5,
				Next: FieldExtractor{From: []string{"selector"}, Selector: "a[rel=next]", Attr: "href", Transform: []string{"absolute_url"}}}},
		Detail: DetailRule{RecordSource: "json_ld", Title: jl("$.title"), Description: jl("$.description"),
			IssuingEntity: jl("$.hiringOrganization.name"), ApplyURL: jl("$.url"), AnchorCountry: jl("$.jobLocation.address.addressCountry")},
	}
	src := jobSource()
	src.BaseURL = srv.URL
	e := NewExecutor(r, httptestFetcher{client: srv.Client()})

	all, err := drain(t, e, src)
	require.NoError(t, err)
	require.Len(t, all, 2) // one detail per listing page, two pages
}

// drain walks the executor's page sequence to exhaustion and returns every item.
func drain(t *testing.T, e *Executor, src domain.Source) ([]domain.ExternalOpportunity, error) {
	t.Helper()
	var all []domain.ExternalOpportunity
	st := PageState{}
	for {
		items, _, _, next, done, err := e.Page(context.Background(), src, st)
		if err != nil {
			return all, err
		}
		all = append(all, items...)
		if done {
			return all, nil
		}
		st = next
	}
}
```

Note: the test references `domain` — add `"github.com/stawi-opportunities/opportunities/pkg/domain"` to the test's imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/recipe/ -run TestExecutor_Crawl -v`
Expected: FAIL — `undefined: PageState` / `e.Page undefined`.

- [ ] **Step 3: Add `PageState` + `Page` to `pkg/recipe/executor.go`**

Add `"strconv"` to the import block. Then append:

```go
// PageState carries pagination position between Page calls. The zero value
// means "start at the beginning".
type PageState struct {
	url    string // resolved URL of the page to fetch next (empty -> derive from recipe)
	page   int    // 1-based page number for page_param pagination
	cursor string // opaque cursor for cursor pagination
}

// Page fetches and extracts ONE page (one api response, or one listing page
// plus its detail pages) and computes the next PageState. done is true when
// there are no more pages. This is the unit the CrawlIterator adapter drives.
func (e *Executor) Page(ctx context.Context, src domain.Source, st PageState) (items []domain.ExternalOpportunity, raw []byte, status int, next PageState, done bool, err error) {
	switch e.recipe.Acquisition {
	case "api":
		return e.apiPaged(ctx, src, st)
	default: // structured_data / selectors -> html
		return e.htmlPaged(ctx, src, st)
	}
}

func (e *Executor) apiPaged(ctx context.Context, src domain.Source, st PageState) ([]domain.ExternalOpportunity, []byte, int, PageState, bool, error) {
	pg := st.page
	if pg == 0 {
		pg = 1
	}
	pageURL, err := e.apiURL(src, pg, st.cursor)
	if err != nil {
		return nil, nil, 0, PageState{}, true, err
	}
	items, raw, status, root, err := e.apiPage(ctx, src, pageURL)
	if err != nil {
		return nil, raw, status, PageState{}, true, err
	}

	p := e.recipe.List.Pagination
	maxPages := p.MaxPages
	if maxPages <= 0 {
		maxPages = 1
	}
	switch p.Mode {
	case "page_param":
		if len(items) == 0 || pg >= maxPages {
			return items, raw, status, PageState{}, true, nil
		}
		return items, raw, status, PageState{page: pg + 1}, false, nil
	case "cursor":
		cur := ""
		if !p.Cursor.empty() && root != nil {
			cur = jsonPathScalar(p.Cursor.JSONPath, root)
		}
		if cur == "" || len(items) == 0 || pg >= maxPages {
			return items, raw, status, PageState{}, true, nil
		}
		return items, raw, status, PageState{page: pg + 1, cursor: cur}, false, nil
	default: // "none"
		return items, raw, status, PageState{}, true, nil
	}
}

func (e *Executor) htmlPaged(ctx context.Context, src domain.Source, st PageState) ([]domain.ExternalOpportunity, []byte, int, PageState, bool, error) {
	pg := st.page
	if pg == 0 {
		pg = 1
	}
	listURL := st.url
	if listURL == "" {
		var err error
		if listURL, err = e.htmlListURL(src, pg); err != nil {
			return nil, nil, 0, PageState{}, true, err
		}
	}
	items, raw, status, listPC, err := e.htmlPage(ctx, src, listURL)
	if err != nil {
		return nil, raw, status, PageState{}, true, err
	}

	p := e.recipe.List.Pagination
	maxPages := p.MaxPages
	if maxPages <= 0 {
		maxPages = 1
	}
	switch p.Mode {
	case "next_link":
		nextURL := ""
		if listPC != nil && !p.Next.empty() {
			nextURL, _ = Evaluate(p.Next, listPC)
		}
		if nextURL == "" || pg >= maxPages || !sameHost(src.BaseURL, nextURL) {
			return items, raw, status, PageState{}, true, nil
		}
		return items, raw, status, PageState{url: nextURL, page: pg + 1}, false, nil
	case "page_param":
		if len(items) == 0 || pg >= maxPages {
			return items, raw, status, PageState{}, true, nil
		}
		nextURL, err := e.htmlListURL(src, pg+1)
		if err != nil {
			return items, raw, status, PageState{}, true, nil
		}
		return items, raw, status, PageState{url: nextURL, page: pg + 1}, false, nil
	default: // "none"
		return items, raw, status, PageState{}, true, nil
	}
}

// apiURL builds the endpoint URL for an api page, applying static params plus
// the page_param/cursor for pagination.
func (e *Executor) apiURL(src domain.Source, page int, cursor string) (string, error) {
	u, err := resolveURL(src.BaseURL, e.recipe.List.Endpoint)
	if err != nil {
		return "", err
	}
	q := u.Query()
	for k, v := range e.recipe.List.Params {
		q.Set(k, v)
	}
	p := e.recipe.List.Pagination
	if p.Mode == "page_param" && p.Param != "" {
		q.Set(p.Param, strconv.Itoa(page))
	}
	if p.Mode == "cursor" && p.Param != "" && cursor != "" {
		q.Set(p.Param, cursor)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// htmlListURL builds the listing URL for an html page. For page_param it sets
// the page query param on the base URL; otherwise it uses the base URL as-is.
func (e *Executor) htmlListURL(src domain.Source, page int) (string, error) {
	u, err := resolveURL(src.BaseURL, e.recipe.List.Endpoint) // Endpoint optional; empty -> base
	if err != nil {
		return "", err
	}
	p := e.recipe.List.Pagination
	if p.Mode == "page_param" && p.Param != "" {
		q := u.Query()
		q.Set(p.Param, strconv.Itoa(page))
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

// resolveURL joins a possibly-relative ref against base. An empty ref yields base.
func resolveURL(base, ref string) (*url.URL, error) {
	b, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	if ref == "" {
		return b, nil
	}
	r, err := url.Parse(ref)
	if err != nil {
		return nil, err
	}
	return b.ResolveReference(r), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/recipe/ -run TestExecutor_Crawl -v`
Expected: PASS (both pagination modes).

- [ ] **Step 5: Run the full package + race + vet**

Run: `go test -race ./pkg/recipe/... && go vet ./pkg/recipe/...`
Expected: PASS, no warnings.

- [ ] **Step 6: Commit**

```bash
git add pkg/recipe/executor.go pkg/recipe/executor_paginate_test.go
git commit -m "feat(recipe): Executor pagination (api page_param/cursor, html next_link/page_param) + MaxPages guard"
```

---

## Task 5: `connectors.CrawlIterator` adapter

**Files:**
- Create: `pkg/connectors/recipeconn/recipeconn.go`
- Test: `pkg/connectors/recipeconn/recipeconn_test.go`

- [ ] **Step 1: Write the failing test**

```go
// pkg/connectors/recipeconn/recipeconn_test.go
package recipeconn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/recipe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type httptestFetcher struct{ c *http.Client }

func (f httptestFetcher) Get(ctx context.Context, url string) ([]byte, int, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := f.c.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	var buf []byte
	tmp := make([]byte, 2048)
	for {
		n, rerr := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if rerr != nil {
			break
		}
	}
	return buf, resp.StatusCode, nil
}

func TestIterator_DrivesExecutorLikeThePipeline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`{"jobs":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"jobs":[{"title":"A","description":"A description that comfortably clears the fifty character minimum gate.","company":"X","apply":"https://x.io/a","country":"KE"}]}`))
	}))
	defer srv.Close()

	rec := func(p string) recipe.FieldExtractor { return recipe.FieldExtractor{From: []string{"record"}, JSONPath: p} }
	r := &recipe.Recipe{
		Acquisition: "api",
		Kind:        recipe.KindRule{Mode: "source_default"},
		List: recipe.ListRule{Mode: "api", Endpoint: "/api/jobs", ItemsPath: "$.jobs",
			Pagination: recipe.Pagination{Mode: "page_param", Param: "page", MaxPages: 5}},
		Detail: recipe.DetailRule{RecordSource: "record", Title: rec("$.title"), Description: rec("$.description"),
			IssuingEntity: rec("$.company"), ApplyURL: rec("$.apply"), AnchorCountry: rec("$.country")},
	}
	src := domain.Source{Type: "brightermonday", BaseURL: srv.URL, Country: "NG", Kinds: []string{"job"}}
	src.ID = "src_1"

	exec := recipe.NewExecutor(r, httptestFetcher{c: srv.Client()})
	it := New(exec)

	var all []domain.ExternalOpportunity
	for it.Next(context.Background(), src) {
		all = append(all, it.Items()...)
		assert.Equal(t, 200, it.HTTPStatus())
		assert.NotEmpty(t, it.RawPayload())
	}
	require.NoError(t, it.Err())
	require.Len(t, all, 1)
	assert.Equal(t, "A", all[0].Title)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/connectors/recipeconn/ -v`
Expected: FAIL — `undefined: New`.

- [ ] **Step 3: Write `pkg/connectors/recipeconn/recipeconn.go`**

```go
// Package recipeconn adapts a recipe.Executor to the connectors.CrawlIterator
// contract so the existing crawl pipeline can consume recipe-driven sources
// with no handler changes. (Wiring the executor into source resolution is
// Plan 3, once Source carries its recipe.)
package recipeconn

import (
	"context"

	"github.com/stawi-opportunities/opportunities/pkg/content"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/recipe"
)

// Iterator drives a recipe.Executor page-by-page and exposes each page through
// the connectors.CrawlIterator interface.
type Iterator struct {
	exec *recipe.Executor

	state   recipe.PageState
	started bool
	done    bool

	items   []domain.ExternalOpportunity
	raw     []byte
	status  int
	err     error
}

// New builds an Iterator over the given executor.
func New(exec *recipe.Executor) *Iterator { return &Iterator{exec: exec} }

// Next fetches the next page. It returns false when iteration is complete or an
// error occurred. The source is passed per-call to match how the pipeline
// drives connectors.
func (it *Iterator) Next(ctx context.Context, src domain.Source) bool {
	if it.done || it.err != nil {
		return false
	}
	items, raw, status, next, done, err := it.exec.Page(ctx, src, it.state)
	it.items, it.raw, it.status = items, raw, status
	if err != nil {
		it.err = err
		return false
	}
	it.started = true
	if done {
		it.done = true
	} else {
		it.state = next
	}
	return true
}

func (it *Iterator) Items() []domain.ExternalOpportunity { return it.items }
func (it *Iterator) RawPayload() []byte                  { return it.raw }
func (it *Iterator) HTTPStatus() int                     { return it.status }
func (it *Iterator) Err() error                          { return it.err }

// Content returns nil: recipe extraction works off the raw page directly and
// does not produce a content.Extracted (the pipeline tolerates nil here).
func (it *Iterator) Content() *content.Extracted { return nil }
```

Note: the project's `connectors.CrawlIterator` has a `Next(ctx) bool` (no source arg) and a `Cursor() json.RawMessage` method. This Iterator deliberately uses a `Next(ctx, src)` shape and omits `Cursor` because the executor manages its own pagination state internally and is constructed per-source. Plan 3 supplies the thin shim that satisfies the exact pipeline-facing interface (binding the source and providing a `Cursor()` from `PageState`). Keeping the source-bound, cursor-free core here keeps this package testable without the full handler context; do NOT claim it implements `connectors.CrawlIterator` verbatim.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/connectors/recipeconn/ -v`
Expected: PASS.

- [ ] **Step 5: Run everything + race + vet + build**

Run: `go test -race ./pkg/recipe/... ./pkg/connectors/recipeconn/... && go vet ./pkg/recipe/... ./pkg/connectors/recipeconn/... && go build ./...`
Expected: PASS, no warnings, builds.

- [ ] **Step 6: Commit**

```bash
git add pkg/connectors/recipeconn/recipeconn.go pkg/connectors/recipeconn/recipeconn_test.go
git commit -m "feat(recipeconn): CrawlIterator-style adapter driving the recipe Executor page-by-page"
```

---

## Self-Review

**Spec coverage (this plan = §5B Executor + the two acquisition drivers + the connector adapter):**
- §5B `apiDriver` (records straight from a JSON endpoint, one fetch) → Task 2 (`apiPage`) + Task 4 pagination. ✓
- §5B `htmlDriver` (listing → ItemSelector+Link → fetch each detail → DetailRule) → Task 3 (`htmlPage`/`collectDetailURLs`). ✓
- §5B "assembles ExternalOpportunity field-by-field; Kind via KindRule; anchor_country falls back to source.Country" → Task 1 (`buildOpportunity`/`resolveKind`). ✓
- §5B "Executor stops at producing ExternalOpportunity; existing Verify→Normalize→pipeline path runs as today" → Task 5 adapter yields `[]ExternalOpportunity`; no pipeline changes. ✓
- §6 safety: SSRF guard (same-host endpoints/links) → Task 3 (`sameHost`) + Task 4 (next-link host check); `apply_url` never fetched (it's output) ✓; `Pagination.MaxPages` runaway guard → Task 4. ✓
- Company name/logo/profile folded in → Task 1 (`IssuingEntity` + `Attributes["company_logo_url"]`/`["company_profile"]`). ✓
- Multi-kind determinism (never per-page classifier) → Task 1 `resolveKind` (fixed/by_path/source_default; multi-kind source_default errors). ✓
- **Deferred to Plan 3 (correctly out of scope):** the `Source.extraction_recipe` field, wiring the Executor into `crawl_request_handler` source resolution, the production `Fetcher` adapter over `httpx.Client`, and the exact `connectors.CrawlIterator` shim (source-binding + `Cursor()` from `PageState`). Flagged explicitly in Task 5's note and the header.

**Placeholder scan:** none — every step has complete code and exact commands. Task 5 explicitly documents the interface-shape difference rather than hand-waving it.

**Type consistency:** `buildOpportunity(pc, src, r)` and `resolveKind` (Task 1) are used by `apiPage` (Task 2) and `htmlPage` (Task 3). `NewExecutor`/`Executor` (Task 2) gain `htmlPage` (Task 3) and `Page`/`PageState`/`apiPaged`/`htmlPaged` (Task 4). The adapter (Task 5) calls `exec.Page(ctx, src, state)` returning `(items, raw, status, next, done, err)` — matches Task 4's `Page` signature exactly. `jsonPathScalar` (Plan 1, fieldeval.go) is reused in `apiPaged` cursor handling. `Fetcher.Get(ctx, url) ([]byte, int, error)` is consistent across executor and both test fetchers. `domain.ExternalOpportunity`/`domain.Location`/`domain.Source` field names match the real structs (`ID` via `BaseModel`, `Source SourceType`, `AnchorLocation *Location`).

**Note for the implementer:** `domain.Source.Kinds` is a Postgres string array; `len()` and index access used in `resolveKind` work whether it is declared as `[]string` or `pq.StringArray` (both are slices). If the field type causes a compile issue, convert with `[]string(src.Kinds)` at the call site.
