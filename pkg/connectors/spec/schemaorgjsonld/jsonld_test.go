package schemaorgjsonld_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/spec"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/spec/schemaorgjsonld"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// loadFixture reads testdata/<name> relative to this test file.
func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return body
}

func TestMapJobPosting_FullMapping(t *testing.T) {
	raw := json.RawMessage(`{
		"@type": "JobPosting",
		"title": "Senior Engineer",
		"description": "Build awesome things",
		"datePosted": "2026-05-28",
		"validThrough": "2026-06-28",
		"employmentType": "FULL_TIME",
		"hiringOrganization": {"@type":"Organization","name":"Acme Inc"},
		"jobLocation": {
			"@type":"Place",
			"address": {
				"@type":"PostalAddress",
				"addressCountry":"US",
				"addressLocality":"San Francisco"
			}
		},
		"baseSalary": {
			"@type":"MonetaryAmount",
			"currency":"USD",
			"value": {"@type":"QuantitativeValue","minValue":150000,"maxValue":200000}
		},
		"applicantLocationRequirements": "US",
		"url": "https://acme.example.com/jobs/senior-engineer"
	}`)

	opp, err := schemaorgjsonld.MapJobPosting(raw)
	if err != nil {
		t.Fatalf("MapJobPosting: %v", err)
	}
	if opp.Title != "Senior Engineer" {
		t.Errorf("Title = %q; want Senior Engineer", opp.Title)
	}
	if opp.Description != "Build awesome things" {
		t.Errorf("Description = %q", opp.Description)
	}
	if opp.IssuingEntity != "Acme Inc" {
		t.Errorf("IssuingEntity = %q; want Acme Inc", opp.IssuingEntity)
	}
	if opp.PostedAt == nil || opp.PostedAt.Format("2006-01-02") != "2026-05-28" {
		t.Errorf("PostedAt = %v; want 2026-05-28", opp.PostedAt)
	}
	if opp.Deadline == nil || opp.Deadline.Format("2006-01-02") != "2026-06-28" {
		t.Errorf("Deadline = %v; want 2026-06-28", opp.Deadline)
	}
	if got, _ := opp.Attributes["employment_type"].(string); got != "FULL_TIME" {
		t.Errorf("employment_type = %q; want FULL_TIME", got)
	}
	if opp.AnchorLocation == nil {
		t.Fatalf("AnchorLocation is nil")
	}
	if opp.AnchorLocation.Country != "US" {
		t.Errorf("AnchorLocation.Country = %q; want US", opp.AnchorLocation.Country)
	}
	if opp.AnchorLocation.City != "San Francisco" {
		t.Errorf("AnchorLocation.City = %q; want San Francisco", opp.AnchorLocation.City)
	}
	if opp.AmountMin != 150000 {
		t.Errorf("AmountMin = %v; want 150000", opp.AmountMin)
	}
	if opp.AmountMax != 200000 {
		t.Errorf("AmountMax = %v; want 200000", opp.AmountMax)
	}
	if opp.Currency != "USD" {
		t.Errorf("Currency = %q; want USD", opp.Currency)
	}
	if _, ok := opp.Attributes["applicant_location_requirements"]; !ok {
		t.Errorf("Attributes missing applicant_location_requirements")
	}
	if opp.ApplyURL != "https://acme.example.com/jobs/senior-engineer" {
		t.Errorf("ApplyURL = %q", opp.ApplyURL)
	}
}

func TestMapJobPosting_StringHiringOrg(t *testing.T) {
	raw := json.RawMessage(`{
		"@type":"JobPosting",
		"title":"PM",
		"hiringOrganization":"Acme Inc",
		"url":"https://acme.example.com/jobs/pm"
	}`)
	opp, err := schemaorgjsonld.MapJobPosting(raw)
	if err != nil {
		t.Fatalf("MapJobPosting: %v", err)
	}
	if opp.IssuingEntity != "Acme Inc" {
		t.Errorf("IssuingEntity = %q; want Acme Inc", opp.IssuingEntity)
	}
	if opp.Title != "PM" {
		t.Errorf("Title = %q; want PM", opp.Title)
	}
}

// TestExtractJobPostings_GraphIDRefs covers BrighterMonday/Jobberman-style
// JSON-LD where JobPosting.hiringOrganization is a pure {"@id":...} ref into
// an @graph Organization node. Without resolution, IssuingEntity is empty and
// sitemap/schema_org crawls drop every posting.
func TestExtractJobPostings_GraphIDRefs(t *testing.T) {
	html := []byte(`<!doctype html><html><head>
<script type="application/ld+json">
{
  "@context": "https://schema.org",
  "@graph": [
    {
      "@type": "JobPosting",
      "@id": "https://board.example/#/schema/JobPosting/1",
      "title": "Accountant",
      "description": "Keep the books",
      "hiringOrganization": {"@id": "https://board.example/#/schema/Organization/agency-1"},
      "jobLocation": {
        "@id": "https://board.example/#/schema/Place/loc-1",
        "address": {"addressCountry": "UG", "addressRegion": "Uganda"}
      },
      "url": "https://board.example/listings/accountant-1"
    },
    {
      "@type": "Organization",
      "@id": "https://board.example/#/schema/Organization/agency-1",
      "name": "SPYTECH International"
    },
    {
      "@type": "Place",
      "@id": "https://board.example/#/schema/Place/loc-1",
      "name": "Kampala"
    }
  ]
}
</script></head><body></body></html>`)

	posts := schemaorgjsonld.ExtractJobPostings(html)
	if len(posts) != 1 {
		t.Fatalf("ExtractJobPostings = %d; want 1", len(posts))
	}
	opp, err := schemaorgjsonld.MapJobPosting(posts[0])
	if err != nil {
		t.Fatalf("MapJobPosting: %v", err)
	}
	if opp.Title != "Accountant" {
		t.Errorf("Title = %q; want Accountant", opp.Title)
	}
	if opp.IssuingEntity != "SPYTECH International" {
		t.Errorf("IssuingEntity = %q; want SPYTECH International (graph @id must resolve)", opp.IssuingEntity)
	}
	if opp.AnchorLocation == nil || opp.AnchorLocation.Country != "UG" {
		t.Errorf("AnchorLocation = %+v; want Country=UG from inline address overlay", opp.AnchorLocation)
	}
}

func TestExtractJobPostings_BrighterMondayFixture(t *testing.T) {
	body := loadFixture(t, "brightermonday_graph.html")
	posts := schemaorgjsonld.ExtractJobPostings(body)
	if len(posts) != 1 {
		t.Fatalf("ExtractJobPostings = %d; want 1", len(posts))
	}
	opp, err := schemaorgjsonld.MapJobPosting(posts[0])
	if err != nil {
		t.Fatalf("MapJobPosting: %v", err)
	}
	if opp.Title != "Accountant" {
		t.Errorf("Title = %q; want Accountant", opp.Title)
	}
	if strings.TrimSpace(opp.IssuingEntity) == "" {
		t.Errorf("IssuingEntity empty after graph resolve; raw keys should include hiring org name")
	}
	if opp.IssuingEntity != "SPYTECH international Limited" {
		t.Errorf("IssuingEntity = %q; want SPYTECH international Limited", opp.IssuingEntity)
	}
}

func TestExtractJobPostings_Multiple(t *testing.T) {
	// Indirectly via the connector iterator, which is what callers
	// observe in practice.
	fixture := loadFixture(t, "job_with_jsonld.html")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(fixture)
	}))
	t.Cleanup(srv.Close)

	body := []byte(`
type: schemaorgjsonld
list_url: ` + srv.URL + `/page
fields:
  title: title
`)

	client := httpx.NewClient(5*time.Second, "test/schemaorgjsonld")
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
}

func TestConnector_EmitsAllPostings(t *testing.T) {
	fixture := loadFixture(t, "job_with_jsonld.html")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(fixture)
	}))
	t.Cleanup(srv.Close)

	body := []byte(`
type: schemaorgjsonld
list_url: ` + srv.URL + `/jobs
fields:
  title: title
`)

	client := httpx.NewClient(5*time.Second, "test/schemaorgjsonld")
	c, err := spec.NewFromYAML("example", body, client)
	if err != nil {
		t.Fatalf("NewFromYAML: %v", err)
	}
	it := c.Crawl(context.Background(), domain.Source{
		BaseModel: domain.BaseModel{ID: "src-1"},
		Type:      "example",
	})
	if !it.Next(context.Background()) {
		t.Fatalf("Next: false; err=%v", it.Err())
	}
	items := it.Items()
	if len(items) != 2 {
		t.Fatalf("items = %d; want 2", len(items))
	}

	// Find the senior engineer posting; ordering is "top-level first,
	// then @graph". The senior engineer block is first in the file.
	var senior, pm *domain.ExternalOpportunity
	for i := range items {
		switch items[i].Title {
		case "Senior Engineer":
			senior = &items[i]
		case "Product Manager":
			pm = &items[i]
		}
	}
	if senior == nil {
		t.Fatal("Senior Engineer not found")
	}
	if pm == nil {
		t.Fatal("Product Manager not found")
	}
	if senior.AnchorLocation == nil || senior.AnchorLocation.Country != "US" {
		t.Errorf("Senior AnchorLocation = %+v", senior.AnchorLocation)
	}
	if senior.Currency != "USD" {
		t.Errorf("Senior Currency = %q", senior.Currency)
	}
	if pm.IssuingEntity != "Acme Inc" {
		t.Errorf("PM IssuingEntity = %q", pm.IssuingEntity)
	}
	if pm.ApplyURL != "https://acme.example.com/jobs/pm" {
		t.Errorf("PM ApplyURL = %q", pm.ApplyURL)
	}

	// HTTPStatus + RawPayload + Cursor + Content checks.
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
