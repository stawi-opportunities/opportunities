package recipeconn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/recipe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConnectorIterator_ResumeFromCheckpoint proves a recipe source resumes
// mid-pagination from a persisted cursor instead of restarting from page 1 —
// the property that makes a millions-of-jobs crawl recoverable across slices
// and process restarts.
func TestConnectorIterator_ResumeFromCheckpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		job := func(title string) string {
			return `{"jobs":[{"title":"` + title + `","description":"A description comfortably over the fifty character minimum gate now.","company":"X","apply":"https://x.io/` + title + `","country":"KE"}]}`
		}
		switch req.URL.Query().Get("page") {
		case "1":
			_, _ = w.Write([]byte(job("A")))
		case "2":
			_, _ = w.Write([]byte(job("B")))
		default:
			_, _ = w.Write([]byte(`{"jobs":[]}`))
		}
	}))
	defer srv.Close()

	rec := func(p string) recipe.FieldExtractor {
		return recipe.FieldExtractor{From: []string{"record"}, JSONPath: p}
	}
	r := &recipe.Recipe{Acquisition: "api", Kind: recipe.KindRule{Mode: "source_default"},
		List:   recipe.ListRule{Mode: "api", Endpoint: "/api/jobs", ItemsPath: "$.jobs", Pagination: recipe.Pagination{Mode: "page_param", Param: "page", MaxPages: 10}},
		Detail: recipe.DetailRule{RecordSource: "record", Title: rec("$.title"), Description: rec("$.description"), IssuingEntity: rec("$.company"), ApplyURL: rec("$.apply"), AnchorCountry: rec("$.country")}}
	src := domain.Source{Type: "brightermonday", BaseURL: srv.URL, Country: "NG"}
	src.ID = "s1"
	src.Kinds = []string{"job"}
	exec := recipe.NewExecutor(r, httptestFetcher{c: srv.Client()})

	// Fresh iterator: fetch only page 1 (job A), then checkpoint.
	fresh := NewConnectorIterator(exec, src)
	require.True(t, fresh.Next(context.Background()))
	require.NoError(t, fresh.Err())
	require.Len(t, fresh.Items(), 1)
	assert.Equal(t, "A", fresh.Items()[0].Title)

	cp := fresh.Checkpoint()
	require.NotNil(t, cp)
	assert.Equal(t, 2, cp.PageIdx, "checkpoint points at the next page to fetch")

	// Resume from the checkpoint: must continue at page 2 (job B), NOT re-fetch A.
	resumed, err := NewConnectorIteratorResume(exec, src, cp.Cursor)
	require.NoError(t, err)
	var got []domain.ExternalOpportunity
	for resumed.Next(context.Background()) {
		got = append(got, resumed.Items()...)
	}
	require.NoError(t, resumed.Err())
	require.Len(t, got, 1)
	assert.Equal(t, "B", got[0].Title, "resumed at page 2, skipping the already-crawled page 1")
}

func TestConnectorIterator_SatisfiesCrawlIterator(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`{"jobs":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"jobs":[{"title":"A","description":"A description comfortably over the fifty character minimum gate now.","company":"X","apply":"https://x.io/a","country":"KE"}]}`))
	}))
	defer srv.Close()

	rec := func(p string) recipe.FieldExtractor {
		return recipe.FieldExtractor{From: []string{"record"}, JSONPath: p}
	}
	r := &recipe.Recipe{Acquisition: "api", Kind: recipe.KindRule{Mode: "source_default"},
		List:   recipe.ListRule{Mode: "api", Endpoint: "/api/jobs", ItemsPath: "$.jobs", Pagination: recipe.Pagination{Mode: "page_param", Param: "page", MaxPages: 5}},
		Detail: recipe.DetailRule{RecordSource: "record", Title: rec("$.title"), Description: rec("$.description"), IssuingEntity: rec("$.company"), ApplyURL: rec("$.apply"), AnchorCountry: rec("$.country")}}
	src := domain.Source{Type: "brightermonday", BaseURL: srv.URL, Country: "NG"}
	src.ID = "s1"
	src.Kinds = []string{"job"}

	exec := recipe.NewExecutor(r, httptestFetcher{c: srv.Client()})

	var it connectors.CrawlIterator = NewConnectorIterator(exec, src)

	var all []domain.ExternalOpportunity
	for it.Next(context.Background()) {
		all = append(all, it.Items()...)
	}
	require.NoError(t, it.Err())
	require.Len(t, all, 1)
	assert.Equal(t, "A", all[0].Title)
	assert.Nil(t, it.Content())
}
