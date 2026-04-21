package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeManticoreServer returns a canned Manticore /search response.
func fakeManticoreServer(t *testing.T, respBody string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/search") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(respBody))
	}))
}

func TestManticoreSearchAdapterDecodesHits(t *testing.T) {
	body := `{"took":1,"timed_out":false,"hits":{"total":2,"hits":[
		{"_id":"1","_score":0.92,"_source":{"canonical_id":"can_a","slug":"job-a","title":"Senior Backend","company":"Acme"}},
		{"_id":"2","_score":0.81,"_source":{"canonical_id":"can_b","slug":"job-b","title":"Staff Backend","company":"Beta"}}
	]}}`
	srv := fakeManticoreServer(t, body)
	defer srv.Close()

	adapter, err := NewManticoreSearch(srv.URL, "idx_jobs_rt")
	if err != nil {
		t.Fatalf("NewManticoreSearch: %v", err)
	}

	hits, err := adapter.KNNWithFilters(context.Background(), SearchRequest{
		Vector:           []float32{0.1, 0.2, 0.3},
		Limit:            10,
		RemotePreference: "remote",
		SalaryMinFloor:   70000,
	})
	if err != nil {
		t.Fatalf("KNNWithFilters: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits=%d, want 2", len(hits))
	}
	if hits[0].CanonicalID != "can_a" || hits[0].Score != 0.92 {
		t.Fatalf("hit[0] wrong: %+v", hits[0])
	}
}

func TestManticoreSearchAdapterBuildsKNNQuery(t *testing.T) {
	// Capture the request body to assert the query shape.
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hits":{"hits":[]}}`))
	}))
	defer srv.Close()

	adapter, _ := NewManticoreSearch(srv.URL, "idx_jobs_rt")
	_, _ = adapter.KNNWithFilters(context.Background(), SearchRequest{
		Vector:             []float32{0.5},
		Limit:              20,
		RemotePreference:   "remote",
		SalaryMinFloor:     80000,
		PreferredLocations: []string{"KE", "US"},
	})
	if captured == nil {
		t.Fatal("server saw no request body")
	}
	if captured["index"] != "idx_jobs_rt" {
		t.Fatalf("index=%v, want idx_jobs_rt", captured["index"])
	}
	if _, ok := captured["knn"]; !ok {
		t.Fatalf("expected knn clause, got %+v", captured)
	}
}
