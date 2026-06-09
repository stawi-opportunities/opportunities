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
