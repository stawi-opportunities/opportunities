package recipe

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
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
			_, _ = w.Write([]byte(`{"jobs":[]}`))
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
	src.BaseURL = srv.URL + "/p1"
	e := NewExecutor(r, httptestFetcher{client: srv.Client()})

	all, err := drain(t, e, src)
	require.NoError(t, err)
	require.Len(t, all, 2)
}

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

func TestExecutor_Crawl_APICursor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Query().Get("cur") {
		case "":
			_, _ = w.Write([]byte(`{"next":"c2","jobs":[{"title":"A","description":"A description long enough to pass the fifty character minimum gate ok.","company":"X","apply":"https://x.io/a","country":"KE"}]}`))
		case "c2":
			_, _ = w.Write([]byte(`{"next":"","jobs":[{"title":"B","description":"Another description that also clears the fifty character minimum gate ok.","company":"Y","apply":"https://x.io/b","country":"KE"}]}`))
		default:
			_, _ = w.Write([]byte(`{"jobs":[]}`))
		}
	}))
	defer srv.Close()

	rec := func(p string) FieldExtractor { return FieldExtractor{From: []string{"record"}, JSONPath: p} }
	r := &Recipe{Acquisition: "api", Kind: KindRule{Mode: "source_default"},
		List: ListRule{Mode: "api", Endpoint: "/api/jobs", ItemsPath: "$.jobs",
			Pagination: Pagination{Mode: "cursor", Param: "cur", MaxPages: 10, Cursor: FieldExtractor{From: []string{"record"}, JSONPath: "$.next"}}},
		Detail: DetailRule{RecordSource: "record", Title: rec("$.title"), Description: rec("$.description"),
			IssuingEntity: rec("$.company"), ApplyURL: rec("$.apply"), AnchorCountry: rec("$.country")}}
	src := jobSource()
	src.BaseURL = srv.URL
	e := NewExecutor(r, httptestFetcher{client: srv.Client()})

	all, err := drain(t, e, src)
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "A", all[0].Title)
	assert.Equal(t, "B", all[1].Title)
}

func TestExecutor_Crawl_APICursor_StopsOnRepeat(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"next":"same","jobs":[{"title":"A","description":"A description long enough to pass the fifty character minimum gate ok.","company":"X","apply":"https://x.io/a","country":"KE"}]}`))
	}))
	defer srv.Close()

	rec := func(p string) FieldExtractor { return FieldExtractor{From: []string{"record"}, JSONPath: p} }
	r := &Recipe{Acquisition: "api", Kind: KindRule{Mode: "source_default"},
		List: ListRule{Mode: "api", Endpoint: "/api/jobs", ItemsPath: "$.jobs",
			Pagination: Pagination{Mode: "cursor", Param: "cur", MaxPages: 50, Cursor: FieldExtractor{From: []string{"record"}, JSONPath: "$.next"}}},
		Detail: DetailRule{RecordSource: "record", Title: rec("$.title"), Description: rec("$.description"),
			IssuingEntity: rec("$.company"), ApplyURL: rec("$.apply"), AnchorCountry: rec("$.country")}}
	src := jobSource()
	src.BaseURL = srv.URL
	e := NewExecutor(r, httptestFetcher{client: srv.Client()})

	_, err := drain(t, e, src)
	require.NoError(t, err)
	assert.Equal(t, 2, calls) // stops once the cursor repeats, not at MaxPages
}
