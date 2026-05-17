package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
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
	// Polymorphic schema: hits carry numeric ids + schema columns; the
	// API response surfaces "id" instead of the legacy canonical_id.
	// Aggregations use schema-aligned facet names (categories, geo_scope).
	ts := stubManticore(func(_ map[string]any) string {
		return `{"hits":{"total":2,"hits":[
            {"_id":1,"_source":{"kind":"job","title":"Go Dev","issuing_entity":"Acme","country":"KE","geo_scope":"remote"}},
            {"_id":2,"_source":{"kind":"job","title":"Sr Go","issuing_entity":"Beta","country":"NG","geo_scope":"hybrid"}}
        ]},"aggregations":{
            "categories":{"buckets":[]},
            "country":{"buckets":[{"key":"KE","doc_count":1},{"key":"NG","doc_count":1}]},
            "geo_scope":{"buckets":[]},
            "employment_type":{"buckets":[]},
            "seniority":{"buckets":[]}
        }}`
	})
	defer ts.Close()
	client, _ := searchindex.Open(searchindex.Config{URL: ts.URL})
	jm := newJobsManticore(client)
	h := v2SearchHandler(jm, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/search?q=go&country=KE", nil)
	rr := httptest.NewRecorder()
	h(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	// id is a string in the SPA wire shape ("1", "2") — see
	// ui/app/src/types/search.ts SearchResult.id: string
	require.Contains(t, rr.Body.String(), `"id":"1"`)
	require.Contains(t, rr.Body.String(), `"facets"`)
	// facets are []{key, count}, not a map
	require.Contains(t, rr.Body.String(), `"country":[`)
}

func TestV2Search_FacetsShapedForSPA(t *testing.T) {
	// /api/search emits the five facet families the SPA's Facets type
	// declares: category, remote_type, employment_type, seniority,
	// country. Other Manticore aggregations are dropped because the
	// SPA never reads them. Each family is a []FacetEntry — count-desc.
	ts := stubManticore(func(_ map[string]any) string {
		return `{"hits":{"total":1,"hits":[
            {"_id":1,"_source":{"kind":"scholarship","title":"Climate MSc","country":"DE","geo_scope":"remote"}}
        ]},"aggregations":{
            "kind":{"buckets":[{"key":"scholarship","doc_count":1}]},
            "categories":{"buckets":[{"key":"42","doc_count":1}]},
            "country":{"buckets":[{"key":"DE","doc_count":1}]},
            "geo_scope":{"buckets":[{"key":"remote","doc_count":1}]},
            "employment_type":{"buckets":[{"key":"full-time","doc_count":1}]},
            "seniority":{"buckets":[{"key":"senior","doc_count":1}]}
        }}`
	})
	defer ts.Close()
	client, _ := searchindex.Open(searchindex.Config{URL: ts.URL})
	jm := newJobsManticore(client)

	dir := t.TempDir()
	writeKindYAML := func(t *testing.T, name, body string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
	writeKindYAML(t, "scholarship.yaml", `kind: scholarship
display_name: Scholarship
issuing_entity_label: Institution
url_prefix: scholarships
search_facets: [field_of_study, degree_level]
`)
	reg, err := opportunity.LoadFromDir(dir)
	require.NoError(t, err)

	h := v2SearchHandler(jm, reg, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v2/search", nil)
	rr := httptest.NewRecorder()
	h(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var resp struct {
		Facets map[string][]struct {
			Key   string `json:"key"`
			Count int    `json:"count"`
		} `json:"facets"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))

	// The five families the SPA's Facets type declares must all be
	// present (empty slice is fine when no buckets matched).
	for _, family := range []string{"category", "remote_type", "employment_type", "seniority", "country"} {
		require.Contains(t, resp.Facets, family, family+" facet family must be present")
	}
	// country bucket from the stubbed aggregation should round-trip.
	require.Equal(t, "DE", resp.Facets["country"][0].Key)
	require.Equal(t, 1, resp.Facets["country"][0].Count)
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
