package recipe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	defer func() { _ = resp.Body.Close() }()
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
