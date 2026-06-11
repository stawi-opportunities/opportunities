package himalayas

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// TestDeepPage429EndsCleanly: the API rate-limiting a crawl deep into
// pagination must end the crawl as a success (jobs already emitted),
// not mark it failed — prod crawls stored 77k jobs and still reported
// iterator_failed on a page-1000+ 429.
func TestDeepPage429EndsCleanly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "1" {
			_, _ = fmt.Fprint(w, `{"jobs":[{"id":1,"title":"Dev","companyName":"Acme","excerpt":"d","applicationLink":"https://x/1"}]}`)
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	oldURL, oldDelay := baseURL, pageDelay
	baseURL, pageDelay = srv.URL, 0
	defer func() { baseURL, pageDelay = oldURL, oldDelay }()

	c := &Connector{client: httpx.NewClient(5*time.Second, "test")}
	it := c.Crawl(context.Background(), domain.Source{})

	if !it.Next(context.Background()) {
		t.Fatalf("page 1 should yield items; err=%v", it.Err())
	}
	if it.Next(context.Background()) {
		t.Fatal("rate-limited page 2 should end iteration")
	}
	if it.Err() != nil {
		t.Fatalf("deep-page 429 must end cleanly, got err=%v", it.Err())
	}
}

// TestPage1429StillFails: a rate limit on the very first page means we
// got nothing — that is a real failure.
func TestPage1429StillFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	oldURL, oldDelay := baseURL, pageDelay
	baseURL, pageDelay = srv.URL, 0
	defer func() { baseURL, pageDelay = oldURL, oldDelay }()

	c := &Connector{client: httpx.NewClient(5*time.Second, "test")}
	it := c.Crawl(context.Background(), domain.Source{})

	if it.Next(context.Background()) {
		t.Fatal("page-1 429 should yield nothing")
	}
	if it.Err() == nil {
		t.Fatal("page-1 429 must surface an error")
	}
}
