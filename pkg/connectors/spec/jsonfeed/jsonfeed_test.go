package jsonfeed_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/spec"
	_ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/jsonfeed"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// singlePageJSON is a typical paginated JSON feed response.
const singlePageJSON = `{
  "data": [
    {
      "id": "abc-1",
      "title": "Senior Engineer",
      "apply_url": "https://example.com/jobs/abc-1",
      "description": {"markdown": "Build distributed systems."},
      "company": {"name": "Acme"},
      "location": "Remote"
    },
    {
      "id": "abc-2",
      "title": "Product Manager",
      "apply_url": "https://example.com/jobs/abc-2",
      "description": {"markdown": "Drive product strategy."},
      "company": {"name": "Globex"},
      "location": "New York, NY"
    }
  ],
  "meta": {"next_cursor": ""}
}`

func TestJSONFeed_ExtractsItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(singlePageJSON))
	}))
	t.Cleanup(srv.Close)

	body := []byte(`
type: jsonfeed
list_url: ` + srv.URL + `/jobs
item_selector: "$.data"
fields:
  title: "$.title"
  apply_url: "$.apply_url"
  description: "$.description.markdown"
  external_id: "$.id"
  company: "$.company.name"
  location: "$.location"
`)

	client := httpx.NewClient(5*time.Second, "test/jsonfeed")
	c, err := spec.NewFromYAML("example", body, client)
	if err != nil {
		t.Fatalf("NewFromYAML: %v", err)
	}

	it := c.Crawl(context.Background(), domain.Source{BaseModel: domain.BaseModel{ID: "src-1"}, Type: "example"})
	if !it.Next(context.Background()) {
		t.Fatalf("Next: false; err=%v", it.Err())
	}

	items := it.Items()
	if len(items) != 2 {
		t.Fatalf("items = %d; want 2", len(items))
	}

	got := items[0]
	if got.Title != "Senior Engineer" {
		t.Errorf("Title = %q; want Senior Engineer", got.Title)
	}
	if got.ApplyURL != "https://example.com/jobs/abc-1" {
		t.Errorf("ApplyURL = %q", got.ApplyURL)
	}
	if got.ExternalID != "abc-1" {
		t.Errorf("ExternalID = %q; want abc-1", got.ExternalID)
	}
	if got.IssuingEntity != "Acme" {
		t.Errorf("IssuingEntity = %q; want Acme", got.IssuingEntity)
	}
	if got.LocationText != "Remote" {
		t.Errorf("LocationText = %q; want Remote", got.LocationText)
	}
	if got.Description != "Build distributed systems." {
		t.Errorf("Description = %q", got.Description)
	}

	if it.HTTPStatus() != 200 {
		t.Errorf("HTTPStatus = %d", it.HTTPStatus())
	}
	if len(it.RawPayload()) == 0 {
		t.Error("RawPayload empty")
	}
	if it.Content() != nil {
		t.Errorf("Content = %v; want nil", it.Content())
	}

	// No more pages (no cursor pagination configured).
	if it.Next(context.Background()) {
		t.Error("Next: true past end; want false")
	}
}

func TestJSONFeed_CursorPagination(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		cursor := r.URL.Query().Get("cursor")
		w.Header().Set("Content-Type", "application/json")

		switch cursor {
		case "":
			// page 1 → cursor "p2"
			_, _ = fmt.Fprintf(w, `{
  "data": [{"id": "1", "title": "Job One"}],
  "meta": {"next_cursor": "p2"}
}`)
		case "p2":
			// page 2 → cursor "p3"
			_, _ = fmt.Fprintf(w, `{
  "data": [{"id": "2", "title": "Job Two"}],
  "meta": {"next_cursor": "p3"}
}`)
		case "p3":
			// page 3 → empty cursor signals end.
			_, _ = fmt.Fprintf(w, `{
  "data": [{"id": "3", "title": "Job Three"}],
  "meta": {"next_cursor": ""}
}`)
		default:
			http.Error(w, "unexpected cursor", http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)

	body := []byte(`
type: jsonfeed
list_url: ` + srv.URL + `/jobs?cursor={cursor}
pagination:
  kind: cursor
  cursor_path: "$.meta.next_cursor"
  initial_cursor: ""
item_selector: "$.data"
fields:
  title: "$.title"
  external_id: "$.id"
`)

	client := httpx.NewClient(5*time.Second, "test/jsonfeed")
	c, err := spec.NewFromYAML("paginated", body, client)
	if err != nil {
		t.Fatalf("NewFromYAML: %v", err)
	}

	it := c.Crawl(context.Background(), domain.Source{BaseModel: domain.BaseModel{ID: "src-2"}, Type: "paginated"})

	var pages int
	var titles []string
	for it.Next(context.Background()) {
		pages++
		for _, item := range it.Items() {
			titles = append(titles, item.Title)
		}
	}
	if err := it.Err(); err != nil {
		t.Fatalf("err: %v", err)
	}

	if pages != 3 {
		t.Errorf("pages = %d; want 3", pages)
	}
	if hits.Load() != 3 {
		t.Errorf("http hits = %d; want 3", hits.Load())
	}
	want := []string{"Job One", "Job Two", "Job Three"}
	if len(titles) != len(want) {
		t.Fatalf("titles = %v; want %v", titles, want)
	}
	for i := range want {
		if titles[i] != want[i] {
			t.Errorf("titles[%d] = %q; want %q", i, titles[i], want[i])
		}
	}
}

func TestJSONFeed_CursorRoundTrips(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"x","title":"T"}],"meta":{"next_cursor":"abc"}}`))
	}))
	t.Cleanup(srv.Close)

	body := []byte(`
type: jsonfeed
list_url: ` + srv.URL + `/jobs?cursor={cursor}
pagination:
  kind: cursor
  cursor_path: "$.meta.next_cursor"
  initial_cursor: ""
item_selector: "$.data"
fields:
  title: "$.title"
`)

	client := httpx.NewClient(5*time.Second, "test/jsonfeed")
	c, _ := spec.NewFromYAML("crs", body, client)
	it := c.Crawl(context.Background(), domain.Source{BaseModel: domain.BaseModel{ID: "src-3"}, Type: "crs"})

	if !it.Next(context.Background()) {
		t.Fatalf("Next: false; err=%v", it.Err())
	}

	raw := it.Cursor()
	if len(raw) == 0 {
		t.Fatal("Cursor() empty; want non-nil")
	}

	var token string
	if err := json.Unmarshal(raw, &token); err != nil {
		t.Fatalf("unmarshal cursor: %v", err)
	}
	if token != "abc" {
		t.Errorf("cursor token = %q; want abc", token)
	}
}
