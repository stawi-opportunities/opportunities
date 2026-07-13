package structured_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/structured"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

func TestHTMLJSONLD_ExtractsJobPosting(t *testing.T) {
	t.Parallel()
	html := `<!doctype html><html><head>
<script type="application/ld+json">
{"@type":"JobPosting","title":"Senior Go Engineer",
 "description":"We need a strong Go engineer to build distributed systems end to end for our platform team.",
 "hiringOrganization":{"@type":"Organization","name":"Acme Co"},
 "url":"https://example.com/jobs/1",
 "jobLocation":{"address":{"addressCountry":"KE","addressLocality":"Nairobi"}}}
</script></head><body></body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(html))
	}))
	t.Cleanup(srv.Close)

	client := httpx.NewClient(10*time.Second, "test")
	c := structured.NewHTMLJSONLD(client, domain.SourceSchemaOrg)
	src := domain.Source{Type: domain.SourceSchemaOrg, BaseURL: srv.URL}
	src.ID = "src_test"

	iter := c.Crawl(context.Background(), src)
	require.True(t, iter.Next(context.Background()))
	items := iter.Items()
	require.Len(t, items, 1)
	require.Equal(t, "Senior Go Engineer", items[0].Title)
	require.Equal(t, "Acme Co", items[0].IssuingEntity)
	require.Equal(t, "https://example.com/jobs/1", items[0].ApplyURL)
	require.NotEmpty(t, items[0].Description)
	require.False(t, iter.Next(context.Background()))
}

func TestHTMLJSONLD_NoJSONLD_Empty(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body><a href="/job/1">Job</a></body></html>`))
	}))
	t.Cleanup(srv.Close)

	client := httpx.NewClient(10*time.Second, "test")
	c := structured.NewHTMLJSONLD(client, domain.SourceGenericHTML)
	src := domain.Source{Type: domain.SourceGenericHTML, BaseURL: srv.URL}
	iter := c.Crawl(context.Background(), src)
	// No structured postings → Next returns false (no stubs).
	require.False(t, iter.Next(context.Background()))
}
