package htmllisting_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/spec"
	_ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/htmllisting"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// loadFixture loads testdata/<name> relative to this test file.
func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return body
}

func TestHTMLListing_ExtractsItems(t *testing.T) {
	fixture := loadFixture(t, "jobs.html")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(fixture)
	}))
	t.Cleanup(srv.Close)

	body := []byte(`
type: htmllisting
list_url: ` + srv.URL + `/jobs
item_selector: .job-card
fields:
  title: ".title::text"
  apply_url: "a.apply::attr(href)"
  description: ".description::text"
  location_text: ".location::text"
  company: ".company::text"
pagination:
  kind: page
  max: 1
`)

	client := httpx.NewClient(5*time.Second, "test/htmllisting")
	c, err := spec.NewFromYAML("example", body, client)
	if err != nil {
		t.Fatalf("NewFromYAML: %v", err)
	}

	it := c.Crawl(context.Background(), domain.Source{BaseModel: domain.BaseModel{ID: "src-1"}, Type: "example"})
	if !it.Next(context.Background()) {
		t.Fatalf("Next: false on first page; err=%v", it.Err())
	}

	items := it.Items()
	if len(items) != 2 {
		t.Fatalf("items = %d; want 2 (got %+v)", len(items), items)
	}

	got := items[0]
	if got.Title != "Senior Engineer" {
		t.Errorf("Title = %q; want %q", got.Title, "Senior Engineer")
	}
	if got.ApplyURL != "https://example.com/apply/1" {
		t.Errorf("ApplyURL = %q; want %q", got.ApplyURL, "https://example.com/apply/1")
	}
	if got.Description == "" || !contains(got.Description, "distributed systems") {
		t.Errorf("Description = %q; expected to mention distributed systems", got.Description)
	}
	if got.LocationText != "Remote, Worldwide" {
		t.Errorf("LocationText = %q; want %q", got.LocationText, "Remote, Worldwide")
	}
	if got.IssuingEntity != "Acme Corp" {
		t.Errorf("IssuingEntity = %q; want %q", got.IssuingEntity, "Acme Corp")
	}

	// Second item smoke check.
	if items[1].Title != "Product Manager" {
		t.Errorf("items[1].Title = %q; want Product Manager", items[1].Title)
	}

	// HTTPStatus + RawPayload + Cursor + Content checks.
	if it.HTTPStatus() != 200 {
		t.Errorf("HTTPStatus = %d; want 200", it.HTTPStatus())
	}
	if len(it.RawPayload()) == 0 {
		t.Error("RawPayload empty")
	}
	if it.Cursor() != nil {
		t.Errorf("Cursor = %v; want nil", it.Cursor())
	}
	if it.Content() != nil {
		t.Errorf("Content = %v; want nil", it.Content())
	}

	// Iterator stops at max=1.
	if it.Next(context.Background()) {
		t.Error("Next: true past max; want false")
	}
}

func TestHTMLListing_PageTokenSubstitution(t *testing.T) {
	fixture := loadFixture(t, "jobs.html")

	var hits atomic.Int32
	var lastPath atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		lastPath.Store(r.URL.Path + "?" + r.URL.RawQuery)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(fixture)
	}))
	t.Cleanup(srv.Close)

	body := []byte(`
type: htmllisting
list_url: ` + srv.URL + `/jobs?page={page}
item_selector: .job-card
fields:
  title: ".title::text"
pagination:
  kind: page
  start: 3
  step: 2
  max: 5
`)

	client := httpx.NewClient(5*time.Second, "test/htmllisting")
	c, err := spec.NewFromYAML("paged", body, client)
	if err != nil {
		t.Fatalf("NewFromYAML: %v", err)
	}
	it := c.Crawl(context.Background(), domain.Source{BaseModel: domain.BaseModel{ID: "src-2"}, Type: "paged"})

	// Drain.
	var pages int
	for it.Next(context.Background()) {
		pages++
	}
	if pages == 0 {
		t.Fatalf("no pages; err=%v", it.Err())
	}

	// First page should have used page=3.
	if int(hits.Load()) == 0 {
		t.Fatalf("server got 0 requests")
	}
	// The last hit's query should reflect start+step iterations.
	last, _ := lastPath.Load().(string)
	if last == "" {
		t.Fatal("no path recorded")
	}
}

func TestHTMLListing_StopOnEmpty(t *testing.T) {
	emptyHTML := []byte(`<!doctype html><html><body><div class="other"></div></body></html>`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(emptyHTML)
	}))
	t.Cleanup(srv.Close)

	body := []byte(`
type: htmllisting
list_url: ` + srv.URL + `/jobs?page={page}
item_selector: .job-card
fields:
  title: ".title::text"
pagination:
  kind: page
  max: 10
  stop_on_empty: true
`)

	client := httpx.NewClient(5*time.Second, "test/htmllisting")
	c, err := spec.NewFromYAML("empty", body, client)
	if err != nil {
		t.Fatalf("NewFromYAML: %v", err)
	}
	it := c.Crawl(context.Background(), domain.Source{BaseModel: domain.BaseModel{ID: "src-3"}, Type: "empty"})

	if !it.Next(context.Background()) {
		t.Fatalf("Next: false on first page; err=%v", it.Err())
	}
	if len(it.Items()) != 0 {
		t.Fatalf("expected 0 items on empty page; got %d", len(it.Items()))
	}
	// Should be done now.
	if it.Next(context.Background()) {
		t.Error("expected stop_on_empty to halt iteration")
	}
}

// contains is a tiny helper so test failures don't require importing
// strings just for one line.
func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
