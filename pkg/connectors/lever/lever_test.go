package lever

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

func TestCompany(t *testing.T) {
	for in, want := range map[string]string{
		"https://jobs.lever.co/meesho":    "meesho",
		"https://jobs.lever.co/kavak/":    "kavak",
		"https://jobs.lever.co/line-corp": "line-corp",
	} {
		if got := company(in); got != want {
			t.Errorf("company(%q)=%q want %q", in, got, want)
		}
	}
}

func TestCrawl_MapsPostings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"abc","text":"Risk Analyst","country":"IN","hostedUrl":"https://jobs.lever.co/meesho/abc","descriptionPlain":"Own the risk model end to end across the lending portfolio.","createdAt":1757916149833,"workplaceType":"onsite","categories":{"location":"Bangalore","commitment":"Full Time Employee","team":"FS"}}]`))
	}))
	defer srv.Close()
	old := apiBaseURL
	apiBaseURL = srv.URL
	defer func() { apiBaseURL = old }()

	c := New(httpx.NewClient(5*time.Second, "test"))
	it := c.Crawl(context.Background(), domain.Source{BaseURL: "https://jobs.lever.co/meesho"})
	if !it.Next(context.Background()) {
		t.Fatalf("expected a page; err=%v", it.Err())
	}
	items := it.Items()
	if len(items) != 1 {
		t.Fatalf("items=%d want 1", len(items))
	}
	j := items[0]
	if j.Title != "Risk Analyst" || j.IssuingEntity != "meesho" || j.LocationText != "Bangalore" {
		t.Fatalf("bad mapping: %+v", j)
	}
	if j.ApplyURL != "https://jobs.lever.co/meesho/abc" || j.PostedAt == nil {
		t.Fatalf("apply/posted: %+v", j)
	}
}

func TestCrawl_DeadBoardFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	old := apiBaseURL
	apiBaseURL = srv.URL
	defer func() { apiBaseURL = old }()

	c := New(httpx.NewClient(5*time.Second, "test"))
	it := c.Crawl(context.Background(), domain.Source{BaseURL: "https://jobs.lever.co/gone"})
	if it.Next(context.Background()) {
		t.Fatal("dead board must not yield items")
	}
	if it.Err() == nil {
		t.Fatal("dead board must surface an error")
	}
}
