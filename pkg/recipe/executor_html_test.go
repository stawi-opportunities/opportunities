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
			Description:   FieldExtractor{From: []string{"json_ld"}, JSONPath: "$.description"},
			IssuingEntity: FieldExtractor{From: []string{"json_ld"}, JSONPath: "$.c"},
			ApplyURL:      FieldExtractor{From: []string{"json_ld"}, JSONPath: "$.u"},
			AnchorCountry: FieldExtractor{From: []string{"json_ld"}, JSONPath: "$.k"}},
	}
	e := NewExecutor(r, httptestFetcher{client: srv.Client()})
	src := jobSource()
	src.BaseURL = srv.URL

	items, _, _, _, err := e.htmlPage(context.Background(), src, srv.URL+"/jobs")
	require.NoError(t, err)
	assert.Empty(t, items)
}
