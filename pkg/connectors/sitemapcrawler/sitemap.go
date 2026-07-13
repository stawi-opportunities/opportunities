// Package sitemapcrawler provides a connector that discovers job URLs from
// XML sitemaps and extracts structured JobPostings from each detail page
// (schema.org JSON-LD). It does NOT emit URL-only AI stubs.
package sitemapcrawler

import (
	"bufio"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strings"
	"time"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/spec/schemaorgjsonld"
	"github.com/stawi-opportunities/opportunities/pkg/content"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// batchSize is how many sitemap URLs we attempt per Next() call. Each URL
// triggers one detail fetch for structured JSON-LD extraction.
const batchSize = 20

// jobPathPatterns are URL path fragments that indicate a job detail page.
var jobPathPatterns = []string{
	"/listing", "/listings/", "/job/", "/jobs/",
	"/position/", "/vacancy/", "/career/", "/posting/",
	"/opening/", "/adverts/", "/apply/",
}

// --------------------------------------------------------------------------
// XML types for sitemap parsing
// --------------------------------------------------------------------------

type sitemapIndex struct {
	XMLName  xml.Name  `xml:"sitemapindex"`
	Sitemaps []sitemap `xml:"sitemap"`
}

type sitemap struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod"`
}

type urlSet struct {
	XMLName xml.Name     `xml:"urlset"`
	URLs    []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod"`
}

// --------------------------------------------------------------------------
// Connector
// --------------------------------------------------------------------------

// Connector discovers job URLs by crawling XML sitemaps.
type Connector struct {
	client     *httpx.Client
	sourceType domain.SourceType
}

// New creates a Connector with the default SourceSitemap type.
func New(client *httpx.Client) *Connector {
	return &Connector{client: client, sourceType: domain.SourceSitemap}
}

// NewTyped creates a Connector that reports the given SourceType.
func NewTyped(client *httpx.Client, st domain.SourceType) *Connector {
	return &Connector{client: client, sourceType: st}
}

// Type returns the SourceType this connector handles.
func (c *Connector) Type() domain.SourceType { return c.sourceType }

// Crawl begins a sitemap-based crawl of the given source.
// Uses source.LastSeenAt to skip sitemap entries that haven't changed since last crawl.
// Each discovered URL is fetched and mapped via schema.org JobPosting JSON-LD
// — URL-only stubs are never emitted.
func (c *Connector) Crawl(_ context.Context, source domain.Source) connectors.CrawlIterator {
	return &iterator{
		client:     c.client,
		sourceType: c.sourceType,
		baseURL:    strings.TrimRight(source.BaseURL, "/"),
		batchSize:  batchSize,
		lastSeenAt: source.LastSeenAt,
	}
}

// --------------------------------------------------------------------------
// Iterator
// --------------------------------------------------------------------------

type iterator struct {
	client     *httpx.Client
	sourceType domain.SourceType
	baseURL    string
	lastSeenAt *time.Time // skip sitemap entries older than this
	jobURLs    []string   // all discovered job URLs
	pos        int        // current position in jobURLs
	batchSize  int
	raw        []byte
	status     int
	err        error
	done       bool
	jobs       []domain.ExternalOpportunity
}

func (it *iterator) Next(ctx context.Context) bool {
	if it.err != nil || it.done {
		return false
	}

	// First call: discover all job URLs from sitemaps.
	if it.jobURLs == nil {
		if err := it.discover(ctx); err != nil {
			it.err = err
			return false
		}
		util.Log(ctx).
			WithField("url", it.baseURL).
			WithField("count", len(it.jobURLs)).
			Info("sitemapcrawler: discovered job URLs")
		if len(it.jobURLs) == 0 {
			it.done = true
			return false
		}
	}

	// Advance until we fill a non-empty structured batch or run out of URLs.
	// Pages without JobPosting JSON-LD are skipped (no AI stubs).
	for it.pos < len(it.jobURLs) {
		end := it.pos + it.batchSize
		if end > len(it.jobURLs) {
			end = len(it.jobURLs)
		}
		batch := it.jobURLs[it.pos:end]
		it.pos = end

		jobs := make([]domain.ExternalOpportunity, 0, len(batch))
		var lastStatus int
		var lastBody []byte
		structured, skipped := 0, 0
		for _, u := range batch {
			opp, body, status, ok := fetchStructuredJob(ctx, it.client, u, it.sourceType)
			lastStatus, lastBody = status, body
			if !ok || opp == nil {
				skipped++
				continue
			}
			jobs = append(jobs, *opp)
			structured++
		}
		it.raw = lastBody
		it.status = lastStatus
		if structured > 0 || skipped > 0 {
			util.Log(ctx).
				WithField("structured", structured).
				WithField("skipped_no_jsonld", skipped).
				Debug("sitemapcrawler: structured detail batch")
		}
		if len(jobs) == 0 {
			// Entire batch lacked structured data — try next batch rather
			// than returning empty Items() which would look like end-of-iter.
			continue
		}
		it.jobs = jobs
		return true
	}

	it.done = true
	return false
}

// fetchStructuredJob GETs a detail URL and maps the first schema.org
// JobPosting. Returns ok=false when the page has no usable structured data.
func fetchStructuredJob(ctx context.Context, client *httpx.Client, pageURL string, srcType domain.SourceType) (*domain.ExternalOpportunity, []byte, int, bool) {
	if client == nil {
		return nil, nil, 0, false
	}
	body, status, err := client.Get(ctx, pageURL, map[string]string{
		"Accept": "text/html,application/xhtml+xml",
	})
	if err != nil || status != 200 || len(body) == 0 {
		return nil, body, status, false
	}
	postings := schemaorgjsonld.ExtractJobPostings(body)
	if len(postings) == 0 {
		return nil, body, status, false
	}
	opp, mapErr := schemaorgjsonld.MapJobPosting(postings[0])
	if mapErr != nil || opp == nil {
		return nil, body, status, false
	}
	// Identity + apply URL defaults from the page we just fetched.
	opp.Source = srcType
	opp.SourceURL = pageURL
	if strings.TrimSpace(opp.ApplyURL) == "" {
		opp.ApplyURL = pageURL
	}
	if strings.TrimSpace(opp.ExternalID) == "" {
		opp.ExternalID = pageURL
	}
	// Title is required. IssuingEntity is filled by @graph resolution in
	// ExtractJobPostings; when still empty (sparse publishers) use a
	// placeholder so valid JobPostings are not dropped entirely.
	if strings.TrimSpace(opp.Title) == "" {
		return nil, body, status, false
	}
	if strings.TrimSpace(opp.IssuingEntity) == "" {
		opp.IssuingEntity = "Unknown"
	}
	return opp, body, status, true
}

func (it *iterator) Items() []domain.ExternalOpportunity { return it.jobs }
func (it *iterator) RawPayload() []byte                   { return it.raw }
func (it *iterator) HTTPStatus() int                      { return it.status }
func (it *iterator) Err() error                           { return it.err }
func (it *iterator) Cursor() json.RawMessage              { return nil }

// Content returns nil — JobPosting fields are on each item.
func (it *iterator) Content() *content.Extracted { return nil }

// --------------------------------------------------------------------------
// Discovery logic
// --------------------------------------------------------------------------

// discover fetches robots.txt, extracts sitemap URLs, parses all sitemaps
// (including sitemap indexes), and collects job URLs.
func (it *iterator) discover(ctx context.Context) error {
	sitemapURLs := it.findSitemapURLs(ctx)
	if len(sitemapURLs) == 0 {
		return nil
	}

	seen := make(map[string]struct{})
	for _, sURL := range sitemapURLs {
		urls, err := it.parseSitemap(ctx, sURL, 0)
		if err != nil {
			util.Log(ctx).WithError(err).WithField("url", sURL).Warn("sitemapcrawler: parse sitemap failed")
			continue
		}
		for _, u := range urls {
			if _, ok := seen[u]; ok {
				continue
			}
			if isJobURL(u) {
				seen[u] = struct{}{}
				it.jobURLs = append(it.jobURLs, u)
			}
		}
	}

	return nil
}

// findSitemapURLs returns sitemap URLs from robots.txt, falling back to
// the conventional /sitemap.xml location.
func (it *iterator) findSitemapURLs(ctx context.Context) []string {
	robotsURL := it.baseURL + "/robots.txt"
	raw, status, err := it.client.Get(ctx, robotsURL, nil)
	if err == nil && status == 200 {
		urls := parseSitemapDirectives(string(raw))
		if len(urls) > 0 {
			return urls
		}
	}

	// Fallback: try the conventional sitemap location.
	return []string{it.baseURL + "/sitemap.xml"}
}

// parseSitemapDirectives extracts Sitemap: URLs from a robots.txt body.
func parseSitemapDirectives(body string) []string {
	var urls []string
	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "sitemap:") {
			u := strings.TrimSpace(line[len("sitemap:"):])
			if u != "" {
				urls = append(urls, u)
			}
		}
	}
	return urls
}

// parseSitemap fetches a sitemap URL and returns all <loc> entries.
// If the response is a sitemapindex, it recursively fetches each sub-sitemap.
func (it *iterator) parseSitemap(ctx context.Context, sitemapURL string, depth int) ([]string, error) {
	if depth > 3 {
		return nil, nil
	}
	raw, status, err := it.client.Get(ctx, sitemapURL, nil)
	it.raw = raw
	it.status = status
	if err != nil {
		return nil, fmt.Errorf("fetch sitemap %s: %w", sitemapURL, err)
	}
	if status != 200 {
		return nil, fmt.Errorf("sitemap %s returned status %d", sitemapURL, status)
	}

	// Try to parse as sitemapindex first.
	var idx sitemapIndex
	if err := xml.Unmarshal(raw, &idx); err == nil && len(idx.Sitemaps) > 0 {
		var allURLs []string
		for _, sm := range idx.Sitemaps {
			sub, err := it.parseSitemap(ctx, sm.Loc, depth+1)
			if err != nil {
				util.Log(ctx).WithError(err).WithField("url", sm.Loc).Warn("sitemapcrawler: sub-sitemap failed")
				continue
			}
			allURLs = append(allURLs, sub...)
		}
		return allURLs, nil
	}

	// Parse as a regular urlset.
	var us urlSet
	if err := xml.Unmarshal(raw, &us); err != nil {
		return nil, fmt.Errorf("unmarshal urlset from %s: %w", sitemapURL, err)
	}

	urls := make([]string, 0, len(us.URLs))
	for _, u := range us.URLs {
		if u.Loc == "" {
			continue
		}
		// Skip entries older than our last crawl (incremental refresh)
		if it.lastSeenAt != nil && u.LastMod != "" {
			if lastMod, err := time.Parse(time.RFC3339, u.LastMod); err == nil {
				if lastMod.Before(*it.lastSeenAt) {
					continue // unchanged since last crawl
				}
			}
			// Also try date-only format (2026-04-16)
			if lastMod, err := time.Parse("2006-01-02", u.LastMod); err == nil {
				if lastMod.Before(*it.lastSeenAt) {
					continue
				}
			}
		}
		urls = append(urls, u.Loc)
	}
	return urls, nil
}

// isJobURL reports whether u looks like a job detail page URL.
func isJobURL(u string) bool {
	lower := strings.ToLower(u)
	for _, pattern := range jobPathPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}
