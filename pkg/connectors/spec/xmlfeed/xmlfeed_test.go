package xmlfeed_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/spec"
	_ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/xmlfeed"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

const sampleXML = `<?xml version="1.0" encoding="UTF-8"?>
<source>
  <job>
    <title>Senior Engineer</title>
    <url>https://x.example.com/1</url>
    <description>Build things</description>
    <date>2026-05-28</date>
  </job>
  <job>
    <title>PM</title>
    <url>https://x.example.com/2</url>
    <description>Drive things</description>
    <date>2026-05-27</date>
  </job>
</source>`

func TestXMLFeed_ExtractsItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(sampleXML))
	}))
	t.Cleanup(srv.Close)

	body := []byte(`
type: xmlfeed
list_url: ` + srv.URL + `/jobs.xml
item_selector: //source/job
fields:
  title: title/text()
  apply_url: url/text()
  description: description/text()
  posted_at: date/text()
`)

	client := httpx.NewClient(5*time.Second, "test/xmlfeed")
	c, err := spec.NewFromYAML("example", body, client)
	if err != nil {
		t.Fatalf("NewFromYAML: %v", err)
	}
	it := c.Crawl(context.Background(), domain.Source{
		BaseModel: domain.BaseModel{ID: "src-1"},
		Type:      "example",
	})
	if !it.Next(context.Background()) {
		t.Fatalf("Next: false on first batch; err=%v", it.Err())
	}

	items := it.Items()
	if len(items) != 2 {
		t.Fatalf("items = %d; want 2", len(items))
	}

	got := items[0]
	if got.Title != "Senior Engineer" {
		t.Errorf("Title = %q; want Senior Engineer", got.Title)
	}
	if got.ApplyURL != "https://x.example.com/1" {
		t.Errorf("ApplyURL = %q", got.ApplyURL)
	}
	if got.Description != "Build things" {
		t.Errorf("Description = %q", got.Description)
	}
	if got.PostedAt == nil || got.PostedAt.Format("2006-01-02") != "2026-05-28" {
		t.Errorf("PostedAt = %v; want 2026-05-28", got.PostedAt)
	}

	if items[1].Title != "PM" {
		t.Errorf("items[1].Title = %q; want PM", items[1].Title)
	}
	if items[1].ApplyURL != "https://x.example.com/2" {
		t.Errorf("items[1].ApplyURL = %q", items[1].ApplyURL)
	}

	if it.HTTPStatus() != 200 {
		t.Errorf("HTTPStatus = %d; want 200", it.HTTPStatus())
	}
	if len(it.RawPayload()) == 0 {
		t.Error("RawPayload empty")
	}
	if it.Cursor() != nil {
		t.Errorf("Cursor = %v; want nil", it.Cursor())
	}
	if it.Content() != nil {
		t.Errorf("Content = %v; want nil", it.Content())
	}

	if it.Next(context.Background()) {
		t.Error("Next: true on second call; want false")
	}
}

func TestXMLFeed_MissingItemSelectorFallsBackToRoot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?><single><title>Only</title></single>`))
	}))
	t.Cleanup(srv.Close)

	body := []byte(`
type: xmlfeed
list_url: ` + srv.URL + `/one.xml
fields:
  title: //single/title/text()
`)
	client := httpx.NewClient(5*time.Second, "test/xmlfeed")
	c, err := spec.NewFromYAML("example", body, client)
	if err != nil {
		t.Fatalf("NewFromYAML: %v", err)
	}
	it := c.Crawl(context.Background(), domain.Source{
		BaseModel: domain.BaseModel{ID: "src-2"},
		Type:      "example",
	})
	if !it.Next(context.Background()) {
		t.Fatalf("Next: false; err=%v", it.Err())
	}
	items := it.Items()
	if len(items) != 1 {
		t.Fatalf("items = %d; want 1", len(items))
	}
	if items[0].Title != "Only" {
		t.Errorf("Title = %q; want Only", items[0].Title)
	}
}
