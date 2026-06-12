package recipe

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// TestMultiTenantAPI: one source crawls every tenant in turn, skipping a
// dead board, and maps fields from the JSON records.
func TestMultiTenantAPI(t *testing.T) {
	mux := http.NewServeMux()
	board := func(company string) string {
		return fmt.Sprintf(`{"jobs":[{"title":"Engineer","content":"We build great distributed systems here for the whole team daily.","company_name":%q,"absolute_url":"https://x/job/1"}]}`, company)
	}
	mux.HandleFunc("/boards/airbnb/jobs", func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, board("Airbnb")) })
	mux.HandleFunc("/boards/stripe/jobs", func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, board("Stripe")) })
	mux.HandleFunc("/boards/dead/jobs", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rec := &Recipe{
		Acquisition: "api",
		Kind:        KindRule{Mode: "fixed", Fixed: "job"},
		List: ListRule{
			Mode:       "api",
			Endpoint:   srv.URL + "/boards/{tenant}/jobs",
			Tenants:    []string{"airbnb", "dead", "stripe"},
			ItemsPath:  "$.jobs",
			Pagination: Pagination{Mode: "none"},
		},
		Detail: DetailRule{
			Title:         FieldExtractor{From: []string{"record"}, JSONPath: "$.title"},
			Description:   FieldExtractor{From: []string{"record"}, JSONPath: "$.content"},
			IssuingEntity: FieldExtractor{From: []string{"record"}, JSONPath: "$.company_name"},
			ApplyURL:      FieldExtractor{From: []string{"record"}, JSONPath: "$.absolute_url"},
		},
	}
	src := domain.Source{BaseURL: srv.URL, Kinds: []string{"job"}}
	ex := NewExecutor(rec, httptestFetcher{client: srv.Client()})

	companies := map[string]bool{}
	st := PageState{}
	for i := 0; i < 10; i++ {
		items, _, _, next, done, err := ex.Page(context.Background(), src, st)
		if err != nil {
			t.Fatalf("page %d: %v", i, err)
		}
		for _, it := range items {
			companies[it.IssuingEntity] = true
		}
		if done {
			break
		}
		st = next
	}
	if !companies["Airbnb"] || !companies["Stripe"] {
		t.Fatalf("expected Airbnb + Stripe across tenants, got %v", companies)
	}
}
