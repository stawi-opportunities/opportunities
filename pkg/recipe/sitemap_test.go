package recipe

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// TestSitemapMode_EnumeratesAndExtracts: a sitemap-index → job-sitemap →
// schema.org detail flow. The list rule enumerates all job URLs from the
// sitemap (filtered by link_pattern), and detail extraction reads JSON-LD.
func TestSitemapMode_EnumeratesAndExtracts(t *testing.T) {
	var base string
	mux := http.NewServeMux()
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `<sitemapindex><sitemap><loc>%s/sitemap-jobs.xml</loc></sitemap></sitemapindex>`, base)
	})
	mux.HandleFunc("/sitemap-jobs.xml", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `<urlset><url><loc>%s/job/1</loc></url><url><loc>%s/job/2</loc></url><url><loc>%s/about</loc></url></urlset>`, base, base, base)
	})
	job := func(title string) string {
		return `<html><head><script type="application/ld+json">{"@type":"JobPosting","title":"` + title + `","description":"We are hiring a great engineer to do great distributed-systems work for the team.","hiringOrganization":{"name":"Acme"},"url":"x","jobLocation":{"address":{"addressCountry":"KE"}}}</script></head><body></body></html>`
	}
	mux.HandleFunc("/job/1", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, job("Engineer One")) })
	mux.HandleFunc("/job/2", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, job("Engineer Two")) })
	mux.HandleFunc("/about", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "<html>about</html>") })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	base = srv.URL

	rec := &Recipe{
		Acquisition: "structured_data",
		Kind:        KindRule{Mode: "fixed", Fixed: "job"},
		List:        ListRule{Mode: "sitemap", Endpoint: "/sitemap.xml", LinkPattern: "/job/", Pagination: Pagination{MaxPages: 5}},
		Detail: DetailRule{
			Title:         FieldExtractor{From: []string{"json_ld"}, JSONPath: "$.title"},
			Description:   FieldExtractor{From: []string{"json_ld"}, JSONPath: "$.description"},
			IssuingEntity: FieldExtractor{From: []string{"json_ld"}, JSONPath: "$.hiringOrganization.name"},
			ApplyURL:      FieldExtractor{From: []string{"page_url"}},
			AnchorCountry: FieldExtractor{From: []string{"json_ld"}, JSONPath: "$.jobLocation.address.addressCountry"},
		},
	}
	src := domain.Source{BaseURL: srv.URL, Kinds: []string{"job"}}
	ex := NewExecutor(rec, httptestFetcher{client: srv.Client()})

	// list rule finds exactly the 2 job URLs (not /about)
	urls, err := ex.ListDetailURLs(context.Background(), src)
	if err != nil || len(urls) != 2 {
		t.Fatalf("ListDetailURLs = %v (err %v); want 2 job URLs", urls, err)
	}
	ex.resetSitemapCache()

	items, _, _, _, done, perr := ex.Page(context.Background(), src, PageState{})
	if perr != nil {
		t.Fatalf("page: %v", perr)
	}
	if len(items) != 2 || !done {
		t.Fatalf("items=%d done=%v; want 2/true", len(items), done)
	}
	if items[0].Title != "Engineer One" || items[0].IssuingEntity != "Acme" {
		t.Fatalf("bad extraction: %+v", items[0])
	}
}
