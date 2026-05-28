package sitemap_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/spec"
	_ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/schemaorgjsonld"
	_ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/sitemap"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// loadFixture reads testdata/<name> relative to this test file.
func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return body
}

func TestSitemap_IncludeExcludeFilters_NoDetailFetch(t *testing.T) {
	sitemapXML := loadFixture(t, "sitemap.xml")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sitemap.xml" {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write(sitemapXML)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	body := []byte(`
type: sitemap
list_url: ` + srv.URL + `/sitemap.xml
sitemap:
  include_patterns: ["/jobs/"]
  exclude_patterns: ["/jobs/product-manager"]
  follow_index: true
  detail_fetch: false
`)

	client := httpx.NewClient(5*time.Second, "test/sitemap")
	c, err := spec.NewFromYAML("example", body, client)
	if err != nil {
		t.Fatalf("NewFromYAML: %v", err)
	}
	it := c.Crawl(context.Background(), domain.Source{
		BaseModel: domain.BaseModel{ID: "src-1"},
		Type:      "example",
	})
	if !it.Next(context.Background()) {
		t.Fatalf("Next: false; err=%v", it.Err())
	}
	items := it.Items()
	if len(items) != 1 {
		t.Fatalf("items = %d; want 1 (after include /jobs/ + exclude product-manager)", len(items))
	}
	if items[0].ApplyURL != "https://example.com/jobs/senior-engineer" {
		t.Errorf("ApplyURL = %q", items[0].ApplyURL)
	}
	// URL-only stub.
	if items[0].Title != "" {
		t.Errorf("Title = %q; want empty for URL-only stub", items[0].Title)
	}

	if it.Next(context.Background()) {
		t.Error("Next: true on second call; want false")
	}
}

func TestSitemap_NoIncludeKeepsAll(t *testing.T) {
	sitemapXML := loadFixture(t, "sitemap.xml")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write(sitemapXML)
	}))
	t.Cleanup(srv.Close)

	body := []byte(`
type: sitemap
list_url: ` + srv.URL + `/sitemap.xml
`)

	client := httpx.NewClient(5*time.Second, "test/sitemap")
	c, err := spec.NewFromYAML("example", body, client)
	if err != nil {
		t.Fatalf("NewFromYAML: %v", err)
	}
	it := c.Crawl(context.Background(), domain.Source{
		BaseModel: domain.BaseModel{ID: "src-2"},
		Type:      "example",
	})
	if !it.Next(context.Background()) {
		t.Fatalf("Next: false; err=%v", it.Err())
	}
	if len(it.Items()) != 3 {
		t.Fatalf("items = %d; want 3 (no filter)", len(it.Items()))
	}
}

func TestSitemap_DetailFetch_SchemaOrgJSONLD(t *testing.T) {
	sitemapXML := loadFixture(t, "sitemap.xml")
	detailHTML := loadFixture(t, "detail-with-jsonld.html")
	var detailHits atomic.Int32

	// One server fields both sitemap.xml AND the /jobs/* detail pages.
	// The sitemap fixture references https://example.com URLs; rewrite
	// those to point at the test server before serving so detail
	// fetches land back here.
	var srvURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		rewritten := strings.ReplaceAll(string(sitemapXML), "https://example.com", srvURL)
		_, _ = w.Write([]byte(rewritten))
	})
	mux.HandleFunc("/jobs/", func(w http.ResponseWriter, _ *http.Request) {
		detailHits.Add(1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(detailHTML)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	srvURL = srv.URL

	body := []byte(`
type: sitemap
list_url: ` + srv.URL + `/sitemap.xml
sitemap:
  include_patterns: ["/jobs/"]
  detail_fetch: true
  detail_fallback_type: schemaorgjsonld
`)

	client := httpx.NewClient(5*time.Second, "test/sitemap")
	c, err := spec.NewFromYAML("example", body, client)
	if err != nil {
		t.Fatalf("NewFromYAML: %v", err)
	}
	it := c.Crawl(context.Background(), domain.Source{
		BaseModel: domain.BaseModel{ID: "src-3"},
		Type:      "example",
	})
	if !it.Next(context.Background()) {
		t.Fatalf("Next: false; err=%v", it.Err())
	}
	items := it.Items()
	if len(items) != 2 {
		t.Fatalf("items = %d; want 2 (jobs included, detail-fetched)", len(items))
	}
	for _, opp := range items {
		if opp.Title != "Senior Engineer" {
			t.Errorf("Title = %q; want Senior Engineer (fixture always returns the same JSON-LD)", opp.Title)
		}
		if opp.IssuingEntity != "Acme Inc" {
			t.Errorf("IssuingEntity = %q", opp.IssuingEntity)
		}
		if opp.ApplyURL == "" {
			t.Error("ApplyURL empty")
		}
	}
	if detailHits.Load() == 0 {
		t.Error("detail server never called")
	}
}

func TestSitemap_RegexInclude(t *testing.T) {
	sitemapXML := loadFixture(t, "sitemap.xml")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write(sitemapXML)
	}))
	t.Cleanup(srv.Close)

	body := []byte(`
type: sitemap
list_url: ` + srv.URL + `/sitemap.xml
sitemap:
  include_patterns: ["re:/jobs/(senior|product)"]
`)

	client := httpx.NewClient(5*time.Second, "test/sitemap")
	c, err := spec.NewFromYAML("example", body, client)
	if err != nil {
		t.Fatalf("NewFromYAML: %v", err)
	}
	it := c.Crawl(context.Background(), domain.Source{
		BaseModel: domain.BaseModel{ID: "src-4"},
		Type:      "example",
	})
	if !it.Next(context.Background()) {
		t.Fatalf("Next: false; err=%v", it.Err())
	}
	if len(it.Items()) != 2 {
		t.Fatalf("items = %d; want 2", len(it.Items()))
	}
}

func TestSitemap_IndexRecursion(t *testing.T) {
	// Two child urlsets behind a sitemapindex.
	indexBody := []byte(`<?xml version="1.0"?>
<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <sitemap><loc>__SRV__/child-a.xml</loc></sitemap>
  <sitemap><loc>__SRV__/child-b.xml</loc></sitemap>
</sitemapindex>`)
	childA := []byte(`<?xml version="1.0"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://example.com/jobs/a</loc></url>
</urlset>`)
	childB := []byte(`<?xml version="1.0"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://example.com/jobs/b</loc></url>
</urlset>`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		switch r.URL.Path {
		case "/index.xml":
			_, _ = w.Write([]byte(strings.ReplaceAll(string(indexBody), "__SRV__", "http://"+r.Host)))
		case "/child-a.xml":
			_, _ = w.Write(childA)
		case "/child-b.xml":
			_, _ = w.Write(childB)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	body := []byte(`
type: sitemap
list_url: ` + srv.URL + `/index.xml
sitemap:
  follow_index: true
`)
	client := httpx.NewClient(5*time.Second, "test/sitemap")
	c, err := spec.NewFromYAML("example", body, client)
	if err != nil {
		t.Fatalf("NewFromYAML: %v", err)
	}
	it := c.Crawl(context.Background(), domain.Source{
		BaseModel: domain.BaseModel{ID: "src-5"},
		Type:      "example",
	})
	if !it.Next(context.Background()) {
		t.Fatalf("Next: false; err=%v", it.Err())
	}
	if len(it.Items()) != 2 {
		t.Fatalf("items = %d; want 2 across both child sitemaps", len(it.Items()))
	}
}
