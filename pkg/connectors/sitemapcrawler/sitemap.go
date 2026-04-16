// Package sitemapcrawler provides a connector that discovers job URLs from
// XML sitemaps. It parses robots.txt for Sitemap directives, handles sitemap
// index files, and filters URLs to only include job-related paths. Each
// discovered URL is returned as an ExternalJob stub; the pipeline's
// NormalizeHandler is responsible for fetching the detail page content.
package sitemapcrawler

import (
	"bufio"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"log"
	"strings"
	"time"

	"stawi.jobs/pkg/connectors"
	"stawi.jobs/pkg/connectors/httpx"
	"stawi.jobs/pkg/content"
	"stawi.jobs/pkg/domain"
)

const batchSize = 50

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
func (c *Connector) Crawl(_ context.Context, source domain.Source) connectors.CrawlIterator {
	return &iterator{
		client:      c.client,
		baseURL:     strings.TrimRight(source.BaseURL, "/"),
		batchSize:   batchSize,
		lastSeenAt:  source.LastSeenAt,
	}
}

// --------------------------------------------------------------------------
// Iterator
// --------------------------------------------------------------------------

type iterator struct {
	client     *httpx.Client
	baseURL    string
	lastSeenAt *time.Time // skip sitemap entries older than this
	jobURLs    []string   // all discovered job URLs
	pos        int        // current position in jobURLs
	batchSize  int
	raw        []byte
	status     int
	err        error
	done       bool
	jobs       []domain.ExternalJob
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
		log.Printf("sitemapcrawler: discovered %d job URLs from %s", len(it.jobURLs), it.baseURL)
		if len(it.jobURLs) == 0 {
			it.done = true
			return false
		}
	}

	// Yield the next batch.
	if it.pos >= len(it.jobURLs) {
		it.done = true
		return false
	}

	end := it.pos + it.batchSize
	if end > len(it.jobURLs) {
		end = len(it.jobURLs)
	}

	batch := it.jobURLs[it.pos:end]
	it.pos = end

	jobs := make([]domain.ExternalJob, 0, len(batch))
	for _, u := range batch {
		jobs = append(jobs, domain.ExternalJob{
			ExternalID: u,
			ApplyURL:   u,
		})
	}
	it.jobs = jobs
	return true
}

func (it *iterator) Jobs() []domain.ExternalJob       { return it.jobs }
func (it *iterator) RawPayload() []byte                { return it.raw }
func (it *iterator) HTTPStatus() int                   { return it.status }
func (it *iterator) Err() error                        { return it.err }
func (it *iterator) Cursor() json.RawMessage           { return nil }

// Content returns nil because sitemap stubs have no page content.
// TODO: The NormalizeHandler will need to detect empty Markdown and fetch
// the detail page itself when processing sitemap-discovered jobs.
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
			log.Printf("sitemapcrawler: error parsing sitemap %s: %v", sURL, err)
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
				log.Printf("sitemapcrawler: sub-sitemap %s: %v", sm.Loc, err)
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
