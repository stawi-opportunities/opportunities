# Plan B2 — Six Spec-Driven Connectors

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Drop a YAML in R2 → new source crawled. Six declarative connector types: `htmllisting`, `jsonfeed`, `rssfeed`, `sitemap`, `schemaorgjsonld`, `xmlfeed`. Operators add a new job board without writing Go, without redeploying.

**Architecture:** A new `pkg/connectors/spec` package contains the dispatch layer + six implementations. Each impl satisfies the existing `connectors.Connector` interface. On boot + on every `definitions.changed.v1` event for `type=connector`, the crawler's `BuildRegistry` reads all `definitions/connector/*.yaml` from the Loader and registers a `*spec.Connector` per spec. The connector's `Crawl(ctx, src)` parses its spec body once and returns an iterator that walks pages applying the spec's selectors.

**Tech Stack:** Go, `goquery` (HTML CSS selectors — already a transitive dep), `github.com/mmcdole/gofeed` (RSS/Atom — new dep), `github.com/PaesslerAG/jsonpath` (JSONPath — new dep), `github.com/antchfx/xmlquery` (XPath — new dep, established library).

**Depends on:** Plan B1 (live as v8.0.63) — `pkg/definitions.Loader` exists; `R2Loader` polls `definitions/connector/*` on the same 5-min ticker as `kind`.

---

## File Structure

**Created:**
- `pkg/connectors/spec/spec.go` — common types (`ConnectorSpec`, `Pagination`, `FieldExtractor`), dispatch.
- `pkg/connectors/spec/spec_test.go` — unit tests for spec parsing + Validate.
- `pkg/connectors/spec/htmllisting/htmllisting.go` — HTML+CSS impl + iterator.
- `pkg/connectors/spec/htmllisting/htmllisting_test.go` — golden tests with fixture HTML.
- `pkg/connectors/spec/jsonfeed/jsonfeed.go` — JSON+JSONPath impl.
- `pkg/connectors/spec/jsonfeed/jsonfeed_test.go`
- `pkg/connectors/spec/rssfeed/rssfeed.go` — RSS/Atom via gofeed.
- `pkg/connectors/spec/rssfeed/rssfeed_test.go`
- `pkg/connectors/spec/sitemap/sitemap.go` — sitemap.xml walker.
- `pkg/connectors/spec/sitemap/sitemap_test.go`
- `pkg/connectors/spec/schemaorgjsonld/jsonld.go` — JSON-LD JobPosting extractor.
- `pkg/connectors/spec/schemaorgjsonld/jsonld_test.go`
- `pkg/connectors/spec/xmlfeed/xmlfeed.go` — generic XML+XPath.
- `pkg/connectors/spec/xmlfeed/xmlfeed_test.go`
- `cmd/seed-connector-specs/main.go` — one-shot R2 uploader for `definitions/connector/*.yaml` (matches Plan B1's `seed-definitions` pattern; takes a different source directory).

**Modified:**
- `apps/crawler/service/setup.go` — `BuildRegistry` accepts a `definitions.Loader` parameter; after the 13 hand-coded connectors, walks every connector spec from R2 and registers a `spec.NewFromYAML(body, ...)` per entry. Subscribes to `definitions.changed.v1` for live rebuild.
- `pkg/connectors/spec` adds 3 new module deps via `go.mod`/`go.sum`.

---

## Common Spec Shape

Every spec file is YAML. Required: `type`, `list_url`, `fields`. Optional: `pagination`, `delay_ms`, `timeout_ms`, `headers`.

```yaml
type: htmllisting   # one of: htmllisting | jsonfeed | rssfeed | sitemap | schemaorgjsonld | xmlfeed
list_url: https://example.com/jobs?page={page}

# Pagination is optional. kind: page | offset | cursor | next_link | none
pagination:
  kind: page
  start: 1
  step: 1
  max: 50
  stop_on_empty: true

# How to find items on each page. Per-type semantics — see each connector's doc.
item_selector: ".job-item"       # CSS, JSONPath, XPath etc; per type

fields:
  title: ".title::text"
  apply_url: "a.apply::attr(href)"
  description:
    selector: ".description::html"
    parse_as: html_to_markdown
  posted_at:
    selector: ".posted::text"
    parse_as: relative_time     # supported: iso | relative_time | epoch | unix_ms

delay_ms: 250       # politeness throttle between page fetches
timeout_ms: 30000   # per-page HTTP timeout

# Optional extra HTTP headers (e.g. for sites that require Accept-Language).
headers:
  Accept-Language: "en-US,en;q=0.9"
```

---

## Tasks

### Task 1: `pkg/connectors/spec` — common types + dispatch

**Files:**
- Create: `pkg/connectors/spec/spec.go`
- Create: `pkg/connectors/spec/spec_test.go`

- [ ] **Step 1: Write the package skeleton**

```go
// Package spec implements declarative connectors driven by YAML specs
// loaded from definitions/connector/*.yaml. Six concrete types live
// in sibling packages; this file holds the shared interface, the
// common ConnectorSpec shape, and dispatch.
package spec

import (
    "context"
    "fmt"
    "time"

    "github.com/stawi-opportunities/opportunities/pkg/connectors"
    "github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
    "github.com/stawi-opportunities/opportunities/pkg/domain"
    "gopkg.in/yaml.v3"
)

// SpecType identifies which concrete impl handles a given spec.
type SpecType string

const (
    TypeHTMLListing      SpecType = "htmllisting"
    TypeJSONFeed         SpecType = "jsonfeed"
    TypeRSSFeed          SpecType = "rssfeed"
    TypeSitemap          SpecType = "sitemap"
    TypeSchemaOrgJSONLD  SpecType = "schemaorgjsonld"
    TypeXMLFeed          SpecType = "xmlfeed"
)

// ConnectorSpec is the YAML shape loaded from definitions/connector/.
type ConnectorSpec struct {
    Type       SpecType                  `yaml:"type"`
    Name       string                    `yaml:"name,omitempty"`  // optional human-readable; defaults to file stem
    ListURL    string                    `yaml:"list_url"`
    Pagination *Pagination               `yaml:"pagination,omitempty"`
    Items      string                    `yaml:"item_selector,omitempty"`  // CSS/JSONPath/XPath; semantics per type
    Fields     map[string]FieldExtractor `yaml:"fields"`
    Headers    map[string]string         `yaml:"headers,omitempty"`
    DelayMS    int                       `yaml:"delay_ms,omitempty"`
    TimeoutMS  int                       `yaml:"timeout_ms,omitempty"`
}

// Pagination drives how the iterator walks pages.
type Pagination struct {
    Kind         string `yaml:"kind"`           // page | offset | cursor | next_link | none
    Start        int    `yaml:"start,omitempty"`
    Step         int    `yaml:"step,omitempty"`
    Max          int    `yaml:"max,omitempty"`
    CursorPath   string `yaml:"cursor_path,omitempty"`     // for JSON cursor pagination
    InitialCursor string `yaml:"initial_cursor,omitempty"`
    StopOnEmpty  bool   `yaml:"stop_on_empty,omitempty"`
}

// FieldExtractor accepts either a string shorthand ("selector::text")
// or a struct with selector + parse_as.
type FieldExtractor struct {
    Selector string `yaml:"selector"`
    ParseAs  string `yaml:"parse_as,omitempty"` // iso | relative_time | epoch | unix_ms | html_to_markdown
}

// UnmarshalYAML accepts either "string" or "{selector, parse_as}".
func (f *FieldExtractor) UnmarshalYAML(value *yaml.Node) error {
    if value.Kind == yaml.ScalarNode {
        f.Selector = value.Value
        return nil
    }
    var aux struct {
        Selector string `yaml:"selector"`
        ParseAs  string `yaml:"parse_as,omitempty"`
    }
    if err := value.Decode(&aux); err != nil {
        return err
    }
    f.Selector = aux.Selector
    f.ParseAs = aux.ParseAs
    return nil
}

// Validate checks the shape of a parsed spec. Returns an error per
// the first problem found; never partial.
func (s *ConnectorSpec) Validate() error {
    if s.Type == "" {
        return fmt.Errorf("spec.type required")
    }
    switch s.Type {
    case TypeHTMLListing, TypeJSONFeed, TypeRSSFeed, TypeSitemap, TypeSchemaOrgJSONLD, TypeXMLFeed:
    default:
        return fmt.Errorf("spec.type %q unknown", s.Type)
    }
    if s.ListURL == "" {
        return fmt.Errorf("spec.list_url required")
    }
    if len(s.Fields) == 0 && s.Type != TypeSitemap {
        return fmt.Errorf("spec.fields required for type %s", s.Type)
    }
    if s.Pagination != nil {
        switch s.Pagination.Kind {
        case "page", "offset", "cursor", "next_link", "none", "":
        default:
            return fmt.Errorf("spec.pagination.kind %q unknown", s.Pagination.Kind)
        }
    }
    if s.DelayMS < 0 || s.DelayMS > 60_000 {
        return fmt.Errorf("spec.delay_ms must be 0..60000")
    }
    if s.TimeoutMS < 0 || s.TimeoutMS > 120_000 {
        return fmt.Errorf("spec.timeout_ms must be 0..120000")
    }
    return nil
}

// ParseSpec parses YAML into a ConnectorSpec and validates.
func ParseSpec(body []byte) (*ConnectorSpec, error) {
    var s ConnectorSpec
    if err := yaml.Unmarshal(body, &s); err != nil {
        return nil, fmt.Errorf("spec yaml: %w", err)
    }
    if err := s.Validate(); err != nil {
        return nil, err
    }
    return &s, nil
}

// Connector implements connectors.Connector by dispatching to the
// per-type impl. Constructed once per spec file by NewFromYAML.
type Connector struct {
    spec   *ConnectorSpec
    name   string
    client *httpx.Client
    impl   Impl
}

// Impl is the per-type contract the dispatch calls. The six
// concrete impls live in sibling packages; this package only
// holds a Register function they call from init().
type Impl interface {
    Crawl(ctx context.Context, src domain.Source, client *httpx.Client, spec *ConnectorSpec) connectors.CrawlIterator
}

var registry = map[SpecType]Impl{}

// Register is called from each impl package's init().
func Register(t SpecType, impl Impl) {
    registry[t] = impl
}

// NewFromYAML constructs a Connector from a spec body. The connector's
// Type() reflects the spec's name field (or the file stem set by the
// caller). Returns an error if the spec is malformed OR no impl is
// registered for the spec's type.
func NewFromYAML(name string, body []byte, client *httpx.Client) (*Connector, error) {
    s, err := ParseSpec(body)
    if err != nil {
        return nil, fmt.Errorf("connector %s: %w", name, err)
    }
    impl, ok := registry[s.Type]
    if !ok {
        return nil, fmt.Errorf("connector %s: no impl registered for type %q", name, s.Type)
    }
    if s.Name != "" {
        name = s.Name
    }
    return &Connector{spec: s, name: name, client: client, impl: impl}, nil
}

func (c *Connector) Type() domain.SourceType { return domain.SourceType(c.name) }

func (c *Connector) Crawl(ctx context.Context, src domain.Source) connectors.CrawlIterator {
    return c.impl.Crawl(ctx, src, c.client, c.spec)
}
```

Tests in `spec_test.go`:

```go
package spec_test

import (
    "testing"

    "github.com/stawi-opportunities/opportunities/pkg/connectors/spec"
)

func TestParseSpec_Shorthand(t *testing.T) {
    body := []byte(`
type: htmllisting
list_url: https://example.com/jobs
fields:
  title: ".title::text"
`)
    s, err := spec.ParseSpec(body)
    if err != nil {
        t.Fatalf("ParseSpec: %v", err)
    }
    if s.Fields["title"].Selector != ".title::text" {
        t.Fatalf("shorthand not parsed: %+v", s.Fields["title"])
    }
}

func TestParseSpec_StructForm(t *testing.T) {
    body := []byte(`
type: htmllisting
list_url: https://example.com/jobs
fields:
  posted_at:
    selector: ".posted::text"
    parse_as: relative_time
`)
    s, _ := spec.ParseSpec(body)
    if s.Fields["posted_at"].ParseAs != "relative_time" {
        t.Fatalf("parse_as not parsed: %+v", s.Fields["posted_at"])
    }
}

func TestValidate_MissingListURL(t *testing.T) {
    body := []byte(`
type: htmllisting
fields:
  title: ".title"
`)
    if _, err := spec.ParseSpec(body); err == nil {
        t.Fatal("expected error for missing list_url")
    }
}

func TestValidate_UnknownType(t *testing.T) {
    body := []byte(`
type: gibberish
list_url: https://x
fields:
  title: ".title"
`)
    if _, err := spec.ParseSpec(body); err == nil {
        t.Fatal("expected error for unknown type")
    }
}
```

- [ ] **Step 2: Build + test + commit**

```bash
cd /home/j/code/stawi.opportunities
go build ./pkg/connectors/spec/... 2>&1 | tail -5
go test ./pkg/connectors/spec/ -count=1 -v 2>&1 | tail -15
git add pkg/connectors/spec/spec.go pkg/connectors/spec/spec_test.go
git commit -m "feat(connectors/spec): common types + dispatch for declarative connectors

Six impls in sibling packages register their Impl on init().
NewFromYAML parses + validates + binds to the right impl.
ConnectorSpec covers HTML/CSS, JSON/JSONPath, RSS, sitemap,
JSON-LD, and XML/XPath via per-type semantics."
```

---

### Task 2: `htmllisting` connector

**Files:**
- Create: `pkg/connectors/spec/htmllisting/htmllisting.go`
- Create: `pkg/connectors/spec/htmllisting/htmllisting_test.go`
- Create: `pkg/connectors/spec/htmllisting/testdata/jobs.html` (fixture)

Use `github.com/PuerkitoBio/goquery` for CSS selectors (already a transitive dep). The selector syntax `selector::text` / `selector::attr(name)` / `selector::html` is parsed inline.

Core iterator: fetch page → `goquery.NewDocumentFromReader` → find items via `spec.Items` selector → for each item, apply each field's selector to extract → emit `domain.ExternalOpportunity`.

Pagination: rewrite `list_url`'s `{page}` token with the current page number; iterate until `Max` or `stop_on_empty`.

```go
package htmllisting

import (
    "context"
    "fmt"
    "strconv"
    "strings"
    "time"

    "github.com/PuerkitoBio/goquery"

    "github.com/stawi-opportunities/opportunities/pkg/connectors"
    "github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
    "github.com/stawi-opportunities/opportunities/pkg/connectors/spec"
    "github.com/stawi-opportunities/opportunities/pkg/domain"
)

func init() { spec.Register(spec.TypeHTMLListing, &impl{}) }

type impl struct{}

func (impl) Crawl(ctx context.Context, src domain.Source, client *httpx.Client, s *spec.ConnectorSpec) connectors.CrawlIterator {
    return &iter{ctx: ctx, src: src, client: client, spec: s, page: paginationStart(s.Pagination)}
}

func paginationStart(p *spec.Pagination) int {
    if p == nil || p.Start == 0 {
        return 1
    }
    return p.Start
}

type iter struct {
    ctx       context.Context
    src       domain.Source
    client    *httpx.Client
    spec      *spec.ConnectorSpec
    page      int
    items     []domain.ExternalOpportunity
    rawBody   []byte
    httpStatus int
    err       error
    done      bool
}

func (it *iter) Next(ctx context.Context) bool {
    if it.done {
        return false
    }
    if it.spec.Pagination != nil && it.spec.Pagination.Max > 0 && it.page > it.spec.Pagination.Max {
        it.done = true
        return false
    }
    if it.spec.DelayMS > 0 && it.page > paginationStart(it.spec.Pagination) {
        time.Sleep(time.Duration(it.spec.DelayMS) * time.Millisecond)
    }

    url := strings.ReplaceAll(it.spec.ListURL, "{page}", strconv.Itoa(it.page))
    body, status, err := it.client.Get(ctx, url, it.spec.Headers)
    if err != nil {
        it.err = err
        it.done = true
        return false
    }
    it.rawBody = body
    it.httpStatus = status
    if status != 200 {
        it.done = true
        return false
    }

    doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
    if err != nil {
        it.err = err
        it.done = true
        return false
    }

    items := []domain.ExternalOpportunity{}
    doc.Find(it.spec.Items).Each(func(_ int, sel *goquery.Selection) {
        opp := domain.ExternalOpportunity{SourceID: it.src.ID, SourceURL: url}
        for name, fe := range it.spec.Fields {
            v := extractField(sel, fe)
            applyField(&opp, name, v)
        }
        items = append(items, opp)
    })
    it.items = items

    if it.spec.Pagination != nil && it.spec.Pagination.StopOnEmpty && len(items) == 0 {
        it.done = true
    }
    it.page += paginationStep(it.spec.Pagination)
    return true
}

func paginationStep(p *spec.Pagination) int {
    if p == nil || p.Step == 0 {
        return 1
    }
    return p.Step
}

func (it *iter) Items() []domain.ExternalOpportunity { return it.items }
func (it *iter) RawPayload() []byte                  { return it.rawBody }
func (it *iter) HTTPStatus() int                      { return it.httpStatus }
func (it *iter) Content() *content.Extracted          { return nil } // optional
func (it *iter) Cursor() []byte                       { return nil }
func (it *iter) Err() error                            { return it.err }

// extractField parses the selector syntax "css::text", "css::attr(name)",
// "css::html", or bare "css" (defaults to text). Returns the first match.
func extractField(sel *goquery.Selection, fe spec.FieldExtractor) string {
    css := fe.Selector
    extractor := "text"
    var attr string
    if idx := strings.Index(css, "::"); idx >= 0 {
        css, extractor = css[:idx], css[idx+2:]
        if strings.HasPrefix(extractor, "attr(") && strings.HasSuffix(extractor, ")") {
            attr = extractor[5 : len(extractor)-1]
            extractor = "attr"
        }
    }
    found := sel.Find(css).First()
    if found.Length() == 0 {
        // Apply to selection itself if not found in subtree.
        found = sel
    }
    var raw string
    switch extractor {
    case "text", "":
        raw = strings.TrimSpace(found.Text())
    case "html":
        raw, _ = found.Html()
    case "attr":
        raw, _ = found.Attr(attr)
    }
    return applyParseAs(raw, fe.ParseAs)
}

func applyParseAs(raw, kind string) string {
    switch kind {
    case "":
        return raw
    case "html_to_markdown":
        // Use existing pkg/content if it has a markdown converter; otherwise return raw.
        return raw
    case "iso", "relative_time", "epoch", "unix_ms":
        return raw // converted at normalize stage by pkg/normalize; keep as string here
    }
    return raw
}

// applyField maps a YAML field name to the right struct field.
// Unknown names land in Attributes (forward-compatible).
func applyField(opp *domain.ExternalOpportunity, name, value string) {
    if value == "" {
        return
    }
    switch name {
    case "title":
        opp.Title = value
    case "description":
        opp.Description = value
    case "issuing_entity", "company":
        opp.IssuingEntity = value
    case "apply_url":
        opp.ApplyURL = value
    case "location_text", "location":
        opp.LocationText = value
    case "posted_at":
        if t, err := time.Parse(time.RFC3339, value); err == nil {
            opp.PostedAt = &t
        }
    default:
        if opp.Attributes == nil {
            opp.Attributes = map[string]any{}
        }
        opp.Attributes[name] = value
    }
}

func _ = fmt.Sprintf // keep fmt import live if needed
```

Tests use a fixture HTML file:

```go
// htmllisting_test.go
package htmllisting_test

import (
    "context"
    "os"
    "testing"

    "github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
    "github.com/stawi-opportunities/opportunities/pkg/connectors/spec"
    _ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/htmllisting"
    "github.com/stawi-opportunities/opportunities/pkg/domain"
)

func TestHTMLListing_ExtractsItems(t *testing.T) {
    fixture, _ := os.ReadFile("testdata/jobs.html")
    // Fake client that returns the fixture for any URL.
    client := httpx.NewFakeClient(map[string]httpx.FakeResponse{
        "https://example.com/jobs": {Body: fixture, Status: 200},
    })
    body := []byte(`
type: htmllisting
list_url: https://example.com/jobs
item_selector: ".job-card"
fields:
  title: ".title::text"
  apply_url: "a.apply::attr(href)"
  description: ".description::text"
pagination:
  kind: page
  max: 1
`)
    c, err := spec.NewFromYAML("example", body, client)
    if err != nil {
        t.Fatalf("NewFromYAML: %v", err)
    }
    it := c.Crawl(context.Background(), domain.Source{ID: "src-1"})
    if !it.Next(context.Background()) {
        t.Fatal("Next: false on first page")
    }
    items := it.Items()
    if len(items) < 1 {
        t.Fatalf("items = %d; want >= 1", len(items))
    }
    if items[0].Title == "" {
        t.Fatal("title empty")
    }
}
```

Fixture at `testdata/jobs.html`:

```html
<!doctype html>
<html><body>
<div class="job-card">
  <h2 class="title">Senior Engineer</h2>
  <a class="apply" href="https://example.com/apply/1">Apply</a>
  <div class="description">We're hiring a senior engineer to build...</div>
</div>
<div class="job-card">
  <h2 class="title">Product Manager</h2>
  <a class="apply" href="https://example.com/apply/2">Apply</a>
  <div class="description">Drive product strategy for our growing team.</div>
</div>
</body></html>
```

If `httpx.NewFakeClient` doesn't exist, add it (or use a real local HTTP server in the test):

```go
// In pkg/connectors/httpx/testing.go (new):
type FakeClient struct {
    Responses map[string]FakeResponse
}
type FakeResponse struct {
    Body   []byte
    Status int
}
func NewFakeClient(rs map[string]FakeResponse) *Client { ... }
```

(Match the existing `httpx.Client` shape — read `pkg/connectors/httpx/client.go` to see how to construct a fake.)

- [ ] **Step (build + commit)**

```bash
go test ./pkg/connectors/spec/htmllisting/ -count=1 -v 2>&1 | tail -15
git add pkg/connectors/spec/htmllisting/ pkg/connectors/httpx/testing.go
git commit -m "feat(connectors/htmllisting): declarative HTML+CSS listing scraper"
```

---

### Task 3: `jsonfeed` connector

**Files:**
- Create: `pkg/connectors/spec/jsonfeed/jsonfeed.go`
- Create: `pkg/connectors/spec/jsonfeed/jsonfeed_test.go`

Use `github.com/PaesslerAG/jsonpath` (~300 LoC dep). `go get github.com/PaesslerAG/jsonpath` first.

Spec example:
```yaml
type: jsonfeed
list_url: https://api.example.com/jobs?cursor={cursor}
pagination:
  kind: cursor
  cursor_path: "$.meta.next_cursor"
  initial_cursor: ""
item_selector: "$.data[*]"
fields:
  title: "$.title"
  apply_url: "$.apply_url"
  description: "$.description.markdown"
```

Iterator: fetch JSON → `jsonpath.Get(spec.Items, parsed)` → for each item, `jsonpath.Get(fieldSelector, item)` → ExternalOpportunity.

Cursor pagination: extract `cursor_path` from response → substitute `{cursor}` in next URL → continue until cursor is empty.

Commit:
```bash
go test ./pkg/connectors/spec/jsonfeed/ -count=1 -v 2>&1 | tail -10
git add pkg/connectors/spec/jsonfeed/ go.mod go.sum
git commit -m "feat(connectors/jsonfeed): declarative JSON+JSONPath feed scraper"
```

---

### Task 4: `rssfeed` connector

**Files:**
- Create: `pkg/connectors/spec/rssfeed/rssfeed.go`
- Create: `pkg/connectors/spec/rssfeed/rssfeed_test.go`

`go get github.com/mmcdole/gofeed`. gofeed parses both RSS 2.0 and Atom transparently.

Spec — fields map RSS item attributes:
```yaml
type: rssfeed
list_url: https://apply.workable.com/api/v1/widget/accounts/example/feed
fields:
  title: title
  apply_url: link
  description: description
  posted_at: pubDate
  company: author
```

The field selector is just the gofeed `Item` field name (`title`, `link`, `description`, `pubDate`, `author`, `categories`, etc.). The iterator fetches the URL once (RSS is single-shot; no pagination), parses with `gofeed.NewParser().ParseString(body)`, and emits one `ExternalOpportunity` per item.

Test with a small RSS fixture; verify `posted_at` is set when `pubDate` resolves.

Commit:
```bash
git add pkg/connectors/spec/rssfeed/ go.mod go.sum
git commit -m "feat(connectors/rssfeed): RSS 2.0 / Atom feed scraper via gofeed"
```

---

### Task 5: `sitemap` connector

**Files:**
- Create: `pkg/connectors/spec/sitemap/sitemap.go`
- Create: `pkg/connectors/spec/sitemap/sitemap_test.go`

Spec — minimal because sitemaps are uniform:
```yaml
type: sitemap
list_url: https://example.com/sitemap.xml
include_patterns: ["/jobs/", "/careers/"]
exclude_patterns: ["/jobs/archive/"]
follow_index: true              # recurse into <sitemapindex>
detail_fetch: true              # GET each candidate URL
detail_fallback_type: schemaorgjsonld
```

The iterator:
1. Fetches sitemap.xml.
2. If `<sitemapindex>`, recursively fetches nested sitemaps.
3. For each `<url><loc>`, filter by include/exclude patterns.
4. If `detail_fetch=true`, GET each detail URL and run the `detail_fallback_type` impl (defaults to `schemaorgjsonld`) on the body to extract a JobPosting; otherwise emit a URL-only stub (`Title: ""`, `ApplyURL: <url>`).

URL-only stubs flow into the existing crawler enrichment pipeline (which uses the LLM extractor as a last resort).

For sitemap parsing, reuse `pkg/connectors/sitemapcrawler` internals OR use `github.com/oxffaa/gopher-parse-sitemap`. Choose whatever the existing Go code already uses to avoid a new dep.

```bash
git add pkg/connectors/spec/sitemap/
git commit -m "feat(connectors/sitemap): declarative sitemap.xml walker"
```

---

### Task 6: `schemaorgjsonld` connector

**Files:**
- Create: `pkg/connectors/spec/schemaorgjsonld/jsonld.go`
- Create: `pkg/connectors/spec/schemaorgjsonld/jsonld_test.go`

This connector is most-aligned with Google's JobPosting structured-data target. Spec is minimal:
```yaml
type: schemaorgjsonld
list_url: https://example.com/jobs/{slug}     # or a single page that has multiple JobPostings
```

Behaviour: fetch URL → goquery → find all `<script type="application/ld+json">` blocks → for each block, JSON-parse → walk the JSON looking for `@type: JobPosting` (or `[@type: JobPosting]` in `@graph`) → for each match, map standard JobPosting fields to `ExternalOpportunity`:

| JobPosting JSON-LD          | ExternalOpportunity   |
|---|---|
| `title`                     | `Title`               |
| `description`               | `Description`         |
| `hiringOrganization.name`   | `IssuingEntity`       |
| `datePosted`                | `PostedAt`            |
| `validThrough`              | `Deadline`            |
| `employmentType`            | `Attributes["employment_type"]` |
| `jobLocation.address.addressCountry` | `AnchorLocation.Country` |
| `baseSalary.value.minValue` | `AmountMin`           |
| `baseSalary.value.maxValue` | `AmountMax`           |
| `baseSalary.currency`       | `Currency`            |
| `applicantLocationRequirements` | `Attributes["applicant_location_requirements"]` |
| top-level `url`             | `ApplyURL`            |

Reusable utility: the same JSON-LD extraction logic will be used by `sitemap` (when `detail_fallback_type=schemaorgjsonld`) AND by the crawler's main path (per the Plan D5 "JSON-LD first" architecture). So factor the JobPosting → ExternalOpportunity mapping into a helper exported as `MapJobPosting(json.RawMessage) (*domain.ExternalOpportunity, error)`.

```bash
git add pkg/connectors/spec/schemaorgjsonld/
git commit -m "feat(connectors/schemaorgjsonld): JSON-LD JobPosting extractor"
```

---

### Task 7: `xmlfeed` connector

**Files:**
- Create: `pkg/connectors/spec/xmlfeed/xmlfeed.go`
- Create: `pkg/connectors/spec/xmlfeed/xmlfeed_test.go`

`go get github.com/antchfx/xmlquery` for XPath. Spec:
```yaml
type: xmlfeed
list_url: https://feed.example.com/jobs.xml
item_xpath: //source/job
fields:
  title: title/text()
  apply_url: url/text()
  description: description/text()
  posted_at: date/text()
```

Iterator: fetch XML → `xmlquery.Parse` → `xmlquery.QueryAll(doc, spec.item_xpath)` → for each node, evaluate field XPaths.

Commit:
```bash
git add pkg/connectors/spec/xmlfeed/ go.mod go.sum
git commit -m "feat(connectors/xmlfeed): generic XML+XPath feed scraper"
```

---

### Task 8: Wire spec connectors into `BuildRegistry`

**Files:**
- Modify: `apps/crawler/service/setup.go`
- Modify: `apps/crawler/cmd/main.go` (pass the loader through).

In `apps/crawler/service/setup.go`:

```go
import (
    "github.com/stawi-opportunities/opportunities/pkg/connectors/spec"
    _ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/htmllisting"
    _ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/jsonfeed"
    _ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/rssfeed"
    _ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/sitemap"
    _ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/schemaorgjsonld"
    _ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/xmlfeed"
    "github.com/stawi-opportunities/opportunities/pkg/definitions"
)

// BuildRegistry now takes a Loader so it can pull connector specs
// from R2 on top of the hand-coded connectors.
func BuildRegistry(ctx context.Context, client *httpx.Client, extractor *extraction.Extractor, loader definitions.Loader) *connectors.Registry {
    reg := connectors.NewRegistry()

    // ... existing 13 connectors (verbatim) ...

    // Spec-driven connectors from R2.
    if loader != nil {
        registerSpecConnectors(ctx, reg, loader, client)
        // Subscribe to definitions.changed.v1 for live rebuild.
        loader.Subscribe(definitions.TypeConnector, func(name, _ string) {
            util.Log(ctx).WithField("name", name).Info("connectors: spec changed, refreshing")
            registerSpecConnectors(ctx, reg, loader, client)
        })
    }
    return reg
}

func registerSpecConnectors(ctx context.Context, reg *connectors.Registry, loader definitions.Loader, client *httpx.Client) {
    entries, err := loader.List(ctx, definitions.TypeConnector)
    if err != nil {
        util.Log(ctx).WithError(err).Warn("connectors: list spec connectors failed")
        return
    }
    for _, e := range entries {
        body, _, err := loader.Get(ctx, definitions.TypeConnector, e.Name)
        if err != nil {
            util.Log(ctx).WithError(err).WithField("name", e.Name).Warn("connectors: get spec failed")
            continue
        }
        c, err := spec.NewFromYAML(e.Name, body, client)
        if err != nil {
            util.Log(ctx).WithError(err).WithField("name", e.Name).Warn("connectors: spec invalid; skipping")
            continue
        }
        reg.Register(c)
    }
}
```

Update `apps/crawler/cmd/main.go` to pass the loader to `BuildRegistry`:

```go
registry := service.BuildRegistry(ctx, httpClient, extractor, loader)
```

(`loader` is the `*definitions.R2Loader` already constructed earlier in main.go from Plan B1-S3.)

Build + tests + commit:

```bash
go build ./... 2>&1 | tail -5
go test ./apps/crawler/... -count=1 -short 2>&1 | tail -10
git add apps/crawler/service/setup.go apps/crawler/cmd/main.go
git commit -m "feat(crawler): register spec-driven connectors from R2 definitions"
```

---

### Task 9: seed sample connector spec for smoke test

**Files:**
- Create: `definitions/connectors/example-htmllisting.yaml` (committed sample).

```yaml
# Example spec — operators copy this as a starting point.
# Doesn't reference a real source; not enabled until an operator
# creates a sources row pointing at the same type name.
type: htmllisting
list_url: https://example.com/careers
item_selector: ".job-listing"
fields:
  title: ".job-title::text"
  apply_url: "a.apply-link::attr(href)"
  description: ".job-description::html"
delay_ms: 500
```

Operators upload connector specs via `PUT /admin/definitions/connector/{name}` (uses the existing Plan B1 endpoint). The crawler picks them up on the next 5-min refresh OR immediately if NATS broadcasts the event.

For initial seed, extend `cmd/seed-definitions/main.go` to optionally upload connector specs too (or add a `--type=connector --prefix=definitions/connector` flag), OR just have operators upload via the admin endpoint manually.

Commit:
```bash
git add definitions/connectors/example-htmllisting.yaml
git commit -m "docs(connectors): example htmllisting spec as starter template"
```

---

### Task 10: Tag + deploy v8.0.64

- [ ] **Push, tag**

```bash
git push origin main
git tag v8.0.64
git push origin v8.0.64
```

- [ ] **Verify after Flux roll**

```bash
# 1. Confirm spec connectors register on crawler boot.
kubectl logs -n product-opportunities -l app.kubernetes.io/name=opportunities-crawler --tail=200 | grep -iE "spec|connector|definitions"

# 2. Upload a real test spec via admin endpoint:
TOKEN=...  # admin JWT
curl -X PUT http://api/admin/definitions/connector/myboard \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/x-yaml" \
  --data-binary @path/to/spec.yaml

# 3. Create a sources row that uses the spec's name as source_type.
# 4. Wait for next scheduler tick.
# 5. Verify: /admin/trace/sources/{id} shows the crawl producing variants.
```

---

## Plan B2 Exit Criteria

- [ ] `pkg/connectors/spec` exports a `Connector` that satisfies `connectors.Connector`.
- [ ] Six concrete impls registered via `init()` in their sibling packages.
- [ ] `BuildRegistry` accepts a `definitions.Loader` and registers all R2-loaded spec connectors.
- [ ] Operator can drop a YAML in R2 via `PUT /admin/definitions/connector/{name}` and have the crawler start using it on the next 5-min tick (or instantly via NATS broadcast).
- [ ] Existing 13 Go connectors still work — no regression in `apps/crawler/service/` tests.
- [ ] One end-to-end manual smoke: upload a spec, create a matching source, see crawl complete.
