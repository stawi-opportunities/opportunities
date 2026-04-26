package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stawi-opportunities/opportunities/pkg/searchindex"
)

func stubManticore(responder func(req map[string]any) string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(responder(body)))
	}))
}

func TestV2Search_ReturnsHitsAndFacets(t *testing.T) {
	ts := stubManticore(func(_ map[string]any) string {
		return `{"hits":{"total":2,"hits":[
            {"_id":1,"_source":{"canonical_id":"c1","slug":"a","title":"Go Dev","company":"Acme","country":"KE","remote_type":"remote","category":"engineering"}},
            {"_id":2,"_source":{"canonical_id":"c2","slug":"b","title":"Sr Go","company":"Beta","country":"NG","remote_type":"hybrid","category":"engineering"}}
        ]},"aggregations":{
            "category":{"buckets":[{"key":"engineering","doc_count":2}]},
            "country":{"buckets":[{"key":"KE","doc_count":1},{"key":"NG","doc_count":1}]},
            "remote_type":{"buckets":[]},
            "employment_type":{"buckets":[]},
            "seniority":{"buckets":[]}
        }}`
	})
	defer ts.Close()
	client, _ := searchindex.Open(searchindex.Config{URL: ts.URL})
	jm := newJobsManticore(client)
	h := v2SearchHandler(jm)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/search?q=go&country=KE", nil)
	rr := httptest.NewRecorder()
	h(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Contains(t, rr.Body.String(), `"canonical_id":"c1"`)
	require.Contains(t, rr.Body.String(), `"facets"`)
}

func TestV2JobByID_NotFound(t *testing.T) {
	ts := stubManticore(func(_ map[string]any) string {
		return `{"hits":{"total":0,"hits":[]}}`
	})
	defer ts.Close()
	client, _ := searchindex.Open(searchindex.Config{URL: ts.URL})
	jm := newJobsManticore(client)
	h := v2JobByIDHandler(jm)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/jobs/missing", nil)
	req.SetPathValue("id", "missing")
	rr := httptest.NewRecorder()
	h(rr, req)

	require.Equal(t, http.StatusNotFound, rr.Code)
}

func TestV2Categories(t *testing.T) {
	ts := stubManticore(func(_ map[string]any) string {
		return `{"hits":{"total":0,"hits":[]},"aggregations":{
            "category":{"buckets":[{"key":"engineering","doc_count":42},{"key":"design","doc_count":10}]},
            "country":{"buckets":[]},"remote_type":{"buckets":[]},"employment_type":{"buckets":[]},"seniority":{"buckets":[]}
        }}`
	})
	defer ts.Close()
	client, _ := searchindex.Open(searchindex.Config{URL: ts.URL})
	jm := newJobsManticore(client)
	h := v2CategoriesHandler(jm)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/categories", nil)
	rr := httptest.NewRecorder()
	h(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.True(t, strings.Contains(rr.Body.String(), `"engineering"`))
}

func TestV2Feed_LocalThenGlobal(t *testing.T) {
	calls := 0
	ts := stubManticore(func(_ map[string]any) string {
		calls++
		if calls == 1 {
			return `{"hits":{"total":1,"hits":[{"_id":1,"_source":{"canonical_id":"c1","slug":"ke-eng","title":"Eng KE","country":"KE"}}]}}`
		}
		return `{"hits":{"total":2,"hits":[
            {"_id":2,"_source":{"canonical_id":"c2","slug":"global-a","title":"A","country":"US"}},
            {"_id":3,"_source":{"canonical_id":"c3","slug":"global-b","title":"B","country":"DE"}}
        ]}}`
	})
	defer ts.Close()
	client, _ := searchindex.Open(searchindex.Config{URL: ts.URL})
	jm := newJobsManticore(client)
	h := v2FeedHandler(jm)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/feed", nil)
	req.Header.Set("CF-IPCountry", "KE")
	rr := httptest.NewRecorder()
	h(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Contains(t, rr.Body.String(), `"local"`)
	require.Contains(t, rr.Body.String(), `"global"`)
	require.Contains(t, rr.Body.String(), `"country":"KE"`)
}
