package rssfeed_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/spec"
	_ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/rssfeed"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

const sampleRSS = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Example Jobs Feed</title>
    <link>https://example.com/jobs</link>
    <description>Latest job postings</description>
    <item>
      <title>Senior Engineer</title>
      <link>https://example.com/jobs/1</link>
      <description>Build distributed systems at scale.</description>
      <pubDate>Wed, 02 Oct 2024 13:00:00 GMT</pubDate>
      <author>jobs@acme.com (Acme Corp)</author>
      <guid>job-001</guid>
    </item>
    <item>
      <title>Product Manager</title>
      <link>https://example.com/jobs/2</link>
      <description>Drive product strategy.</description>
      <pubDate>Wed, 02 Oct 2024 14:00:00 GMT</pubDate>
      <guid>job-002</guid>
    </item>
    <item>
      <title>Designer</title>
      <link>https://example.com/jobs/3</link>
      <description>Design beautiful interfaces.</description>
      <guid>job-003</guid>
    </item>
  </channel>
</rss>`

func TestRSSFeed_ExtractsItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(sampleRSS))
	}))
	t.Cleanup(srv.Close)

	body := []byte(`
type: rssfeed
list_url: ` + srv.URL + `/feed.xml
fields:
  title: title
  apply_url: link
  description: description
  posted_at: pubDate
  company: author
  external_id: guid
`)

	client := httpx.NewClient(5*time.Second, "test/rssfeed")
	c, err := spec.NewFromYAML("example-rss", body, client)
	if err != nil {
		t.Fatalf("NewFromYAML: %v", err)
	}

	it := c.Crawl(context.Background(), domain.Source{BaseModel: domain.BaseModel{ID: "src-1"}, Type: "example-rss"})
	if !it.Next(context.Background()) {
		t.Fatalf("Next: false; err=%v", it.Err())
	}

	items := it.Items()
	if len(items) != 3 {
		t.Fatalf("items = %d; want 3", len(items))
	}

	first := items[0]
	if first.Title != "Senior Engineer" {
		t.Errorf("Title = %q; want Senior Engineer", first.Title)
	}
	if first.ApplyURL != "https://example.com/jobs/1" {
		t.Errorf("ApplyURL = %q", first.ApplyURL)
	}
	if first.Description != "Build distributed systems at scale." {
		t.Errorf("Description = %q", first.Description)
	}
	if first.PostedAt == nil {
		t.Fatal("PostedAt nil; want parsed pubDate")
	}
	wantTime := time.Date(2024, 10, 2, 13, 0, 0, 0, time.UTC)
	if !first.PostedAt.Equal(wantTime) {
		t.Errorf("PostedAt = %v; want %v", first.PostedAt, wantTime)
	}
	if first.IssuingEntity == "" {
		t.Error("IssuingEntity empty; want author")
	}
	if first.ExternalID != "job-001" {
		t.Errorf("ExternalID = %q; want job-001", first.ExternalID)
	}

	// Third item has no pubDate — PostedAt should be nil.
	if items[2].PostedAt != nil {
		t.Errorf("items[2].PostedAt = %v; want nil", items[2].PostedAt)
	}

	if it.HTTPStatus() != 200 {
		t.Errorf("HTTPStatus = %d", it.HTTPStatus())
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

	// Single-shot — second Next must return false.
	if it.Next(context.Background()) {
		t.Error("Next: true past first call; want false")
	}
}

const sampleAtom = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Atom Jobs</title>
  <link href="https://example.com/jobs"/>
  <updated>2024-10-02T13:00:00Z</updated>
  <id>urn:uuid:60a76c80-d399-11d9-b91C-0003939e0af6</id>
  <entry>
    <title>Atom Engineer</title>
    <link href="https://example.com/jobs/a1"/>
    <id>atom-1</id>
    <updated>2024-10-02T13:00:00Z</updated>
    <published>2024-10-02T13:00:00Z</published>
    <summary>Atom feeds work too.</summary>
  </entry>
</feed>`

func TestRSSFeed_HandlesAtom(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		_, _ = w.Write([]byte(sampleAtom))
	}))
	t.Cleanup(srv.Close)

	body := []byte(`
type: rssfeed
list_url: ` + srv.URL + `/feed.atom
fields:
  title: title
  apply_url: link
  description: description
  posted_at: published
`)

	client := httpx.NewClient(5*time.Second, "test/rssfeed")
	c, err := spec.NewFromYAML("atom", body, client)
	if err != nil {
		t.Fatalf("NewFromYAML: %v", err)
	}

	it := c.Crawl(context.Background(), domain.Source{BaseModel: domain.BaseModel{ID: "src-2"}, Type: "atom"})
	if !it.Next(context.Background()) {
		t.Fatalf("Next: false; err=%v", it.Err())
	}

	items := it.Items()
	if len(items) != 1 {
		t.Fatalf("items = %d; want 1", len(items))
	}
	if items[0].Title != "Atom Engineer" {
		t.Errorf("Title = %q", items[0].Title)
	}
	if items[0].ApplyURL != "https://example.com/jobs/a1" {
		t.Errorf("ApplyURL = %q", items[0].ApplyURL)
	}
	if items[0].PostedAt == nil {
		t.Error("PostedAt nil; want parsed Atom published timestamp")
	}
}
