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

	rec := func(p string) recipe.FieldExtractor {
		return recipe.FieldExtractor{From: []string{"record"}, JSONPath: p}
	}
	r := &recipe.Recipe{
		Acquisition: "api",
		Kind:        recipe.KindRule{Mode: "source_default"},
		List: recipe.ListRule{Mode: "api", Endpoint: "/api/jobs", ItemsPath: "$.jobs",
			Pagination: recipe.Pagination{Mode: "page_param", Param: "page", MaxPages: 5}},
		Detail: recipe.DetailRule{RecordSource: "record", Title: rec("$.title"), Description: rec("$.description"),
			IssuingEntity: rec("$.company"), ApplyURL: rec("$.apply"), AnchorCountry: rec("$.country")},
	}
	src := domain.Source{Type: "brightermonday", BaseURL: srv.URL, Country: "NG"}
	src.ID = "src_1"
	src.Kinds = []string{"job"}

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
