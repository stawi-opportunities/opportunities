// Package universal provides an AI-powered connector that discovers job links
// on any HTML listing page using an LLM, replacing per-site regex parsers.
package universal

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/content"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
)

const maxPages = 50

// Connector crawls any HTML job board by using AI to discover job links.
type Connector struct {
	client    *httpx.Client
	extractor *extraction.Extractor
	srcType   domain.SourceType
}

// NewTyped creates a universal Connector that reports the given SourceType.
func NewTyped(client *httpx.Client, extractor *extraction.Extractor, st domain.SourceType) *Connector {
	return &Connector{
		client:    client,
		extractor: extractor,
		srcType:   st,
	}
}

// Type returns the SourceType this connector was created for.
func (c *Connector) Type() domain.SourceType { return c.srcType }

// Crawl starts a crawl of the source. Tries sitemaps first (fastest, most
// complete). Falls back to AI link discovery if no sitemaps found. Falls back
// to pattern matching if AI is unavailable.
func (c *Connector) Crawl(ctx context.Context, source domain.Source) connectors.CrawlIterator {
	// Try sitemap discovery first — most reliable
	sitemapURLs := discoverSitemapJobURLs(ctx, c.client, source.BaseURL, source.LastSeenAt)
	if len(sitemapURLs) > 0 {
		util.Log(ctx).
			WithField("url", source.BaseURL).
			WithField("count", len(sitemapURLs)).
			Info("universal: found job URLs via sitemap")
		return &sitemapIterator{
			jobURLs:   sitemapURLs,
			batchSize: 50,
		}
	}

	// No sitemaps — fall back to AI link discovery
	return &iterator{
		client:    c.client,
		extractor: c.extractor,
		nextURL:   source.BaseURL,
		baseURL:   source.BaseURL,
		page:      0,
	}
}

type iterator struct {
	client    *httpx.Client
	extractor *extraction.Extractor
	nextURL   string
	baseURL   string
	page      int
	jobs      []domain.ExternalOpportunity
	raw       []byte
	status    int
	err       error
	extracted *content.Extracted
}

func (it *iterator) Next(ctx context.Context) bool {
	if it.err != nil || it.nextURL == "" || it.page >= maxPages {
		return false
	}

	raw, status, err := it.client.Get(ctx, it.nextURL, nil)
	it.raw = raw
	it.status = status
	if err != nil {
		util.Log(ctx).WithError(err).
			WithField("page", it.page+1).
			WithField("url", it.nextURL).
			Warn("universal: fetch page failed")
		it.err = err
		return false
	}
	if status != 200 {
		util.Log(ctx).
			WithField("page", it.page+1).
			WithField("status", status).
			WithField("url", it.nextURL).
			Warn("universal: page returned non-200 status")
		it.err = fmt.Errorf("universal: status %d on page %d", status, it.page+1)
		return false
	}

	// Extract main content; use Markdown for AI (cleaner than raw HTML).
	ext, _ := content.ExtractFromHTML(string(raw))
	it.extracted = ext

	// Use Markdown for AI link discovery when available, else fall back to raw.
	discoverInput := string(raw)
	if ext != nil && ext.Markdown != "" {
		discoverInput = ext.Markdown
	}

	links, err := it.extractor.DiscoverLinks(ctx, discoverInput, it.nextURL)
	if err != nil {
		util.Log(ctx).WithError(err).WithField("url", it.nextURL).Warn("universal: AI discover links failed")
		it.err = err
		return false
	}

	// Separate job links from the optional NEXT: pagination link.
	var jobLinks []string
	it.nextURL = ""
	for _, link := range links {
		if strings.HasPrefix(link, "NEXT:") {
			it.nextURL = strings.TrimSpace(strings.TrimPrefix(link, "NEXT:"))
		} else {
			jobLinks = append(jobLinks, link)
		}
	}

	// If AI found no links, fall back to pattern matching
	if len(jobLinks) == 0 {
		jobLinks = patternMatchLinks(string(raw), it.baseURL)
	}

	if len(jobLinks) == 0 {
		return false
	}

	// De-duplicate within this page.
	seen := make(map[string]struct{})
	var jobs []domain.ExternalOpportunity
	for _, link := range jobLinks {
		if _, ok := seen[link]; ok {
			continue
		}
		seen[link] = struct{}{}
		jobs = append(jobs, domain.ExternalOpportunity{
			ExternalID: link,
			ApplyURL:   link,
		})
	}

	it.jobs = jobs
	it.page++
	return true
}

func (it *iterator) Jobs() []domain.ExternalOpportunity       { return it.jobs }
func (it *iterator) RawPayload() []byte                { return it.raw }
func (it *iterator) HTTPStatus() int                   { return it.status }
func (it *iterator) Err() error                        { return it.err }
func (it *iterator) Cursor() json.RawMessage           { return nil }
func (it *iterator) Content() *content.Extracted       { return it.extracted }

var commonJobPatterns = regexp.MustCompile(
	`href=["']([^"']*/(?:jobs?|listings?|vacancies|careers?|positions?)/[^"']+)["']`,
)

func patternMatchLinks(html string, baseURL string) []string {
	matches := commonJobPatterns.FindAllStringSubmatch(html, -1)
	seen := make(map[string]bool)
	var links []string

	// Extract origin from baseURL for resolving relative links
	origin := baseURL
	if idx := strings.Index(baseURL, "://"); idx >= 0 {
		rest := baseURL[idx+3:]
		if slashIdx := strings.Index(rest, "/"); slashIdx >= 0 {
			origin = baseURL[:idx+3+slashIdx]
		}
	}

	for _, m := range matches {
		link := m[1]
		if !strings.HasPrefix(link, "http") {
			if strings.HasPrefix(link, "/") {
				link = origin + link
			} else {
				link = origin + "/" + link
			}
		}
		if !seen[link] {
			seen[link] = true
			links = append(links, link)
		}
	}
	return links
}

// --------------------------------------------------------------------------
// Sitemap-first discovery
// --------------------------------------------------------------------------

// discoverSitemapJobURLs tries to find job URLs via sitemaps. Returns nil
// if no sitemaps are found (caller should fall back to AI discovery).
func discoverSitemapJobURLs(ctx context.Context, client *httpx.Client, baseURL string, lastSeenAt *time.Time) []string {
	base := strings.TrimRight(baseURL, "/")

	// 1. Check robots.txt for Sitemap directives
	sitemapURLs := findSitemapsInRobotsTxt(ctx, client, base)

	// 2. Fallback: try /sitemap.xml
	if len(sitemapURLs) == 0 {
		fallback := base + "/sitemap.xml"
		if raw, status, err := client.Get(ctx, fallback, nil); err == nil && status == 200 && len(raw) > 0 {
			sitemapURLs = []string{fallback}
		}
	}

	if len(sitemapURLs) == 0 {
		return nil
	}

	// 3. Parse all sitemaps and collect job URLs
	seen := make(map[string]bool)
	var jobURLs []string

	for _, smURL := range sitemapURLs {
		urls := parseSitemapRecursive(ctx, client, smURL, lastSeenAt, 0)
		for _, u := range urls {
			if !seen[u] && isJobURL(u) {
				seen[u] = true
				jobURLs = append(jobURLs, u)
			}
		}
	}

	return jobURLs
}

func findSitemapsInRobotsTxt(ctx context.Context, client *httpx.Client, baseURL string) []string {
	raw, status, err := client.Get(ctx, baseURL+"/robots.txt", nil)
	if err != nil || status != 200 {
		return nil
	}
	var urls []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), "sitemap:") {
			u := strings.TrimSpace(line[len("sitemap:"):])
			if u != "" {
				urls = append(urls, u)
			}
		}
	}
	return urls
}

func parseSitemapRecursive(ctx context.Context, client *httpx.Client, sitemapURL string, lastSeenAt *time.Time, depth int) []string {
	if depth > 3 {
		return nil
	}
	raw, status, err := client.Get(ctx, sitemapURL, nil)
	if err != nil || status != 200 {
		return nil
	}

	// Try sitemapindex first
	type smEntry struct {
		Loc string `xml:"loc"`
	}
	type smIndex struct {
		XMLName  xml.Name  `xml:"sitemapindex"`
		Sitemaps []smEntry `xml:"sitemap>loc"`
	}
	var idx smIndex
	if xml.Unmarshal(raw, &idx) == nil && len(idx.Sitemaps) > 0 {
		var all []string
		for _, sm := range idx.Sitemaps {
			all = append(all, parseSitemapRecursive(ctx, client, sm.Loc, lastSeenAt, depth+1)...)
		}
		return all
	}

	// Parse as urlset
	type urlEntry struct {
		Loc     string `xml:"loc"`
		LastMod string `xml:"lastmod"`
	}
	type urlSet struct {
		XMLName xml.Name   `xml:"urlset"`
		URLs    []urlEntry `xml:"url"`
	}
	var us urlSet
	if xml.Unmarshal(raw, &us) != nil {
		return nil
	}

	var urls []string
	for _, u := range us.URLs {
		if u.Loc == "" {
			continue
		}
		// Filter by lastmod if we have a previous crawl time
		if lastSeenAt != nil && u.LastMod != "" {
			if t, err := time.Parse(time.RFC3339, u.LastMod); err == nil && t.Before(*lastSeenAt) {
				continue
			}
			if t, err := time.Parse("2006-01-02", u.LastMod); err == nil && t.Before(*lastSeenAt) {
				continue
			}
			// Also try datetime without timezone
			if t, err := time.Parse("2006-01-02T15:04:05", u.LastMod); err == nil && t.Before(*lastSeenAt) {
				continue
			}
		}
		urls = append(urls, u.Loc)
	}
	return urls
}

var jobPathPatterns = []string{
	"/listing", "/listings/", "/job/", "/jobs/",
	"/position/", "/vacancy/", "/career/", "/posting/",
	"/opening/", "/adverts/", "/apply/",
}

func isJobURL(u string) bool {
	lower := strings.ToLower(u)
	for _, p := range jobPathPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// sitemapIterator yields job URLs discovered from sitemaps in batches.
type sitemapIterator struct {
	jobURLs   []string
	pos       int
	batchSize int
	jobs      []domain.ExternalOpportunity
	raw       []byte
}

func (it *sitemapIterator) Next(_ context.Context) bool {
	if it.pos >= len(it.jobURLs) {
		return false
	}
	end := it.pos + it.batchSize
	if end > len(it.jobURLs) {
		end = len(it.jobURLs)
	}
	batch := it.jobURLs[it.pos:end]
	it.pos = end

	it.jobs = make([]domain.ExternalOpportunity, 0, len(batch))
	for _, u := range batch {
		it.jobs = append(it.jobs, domain.ExternalOpportunity{
			ExternalID: u,
			ApplyURL:   u,
		})
	}
	return true
}

func (it *sitemapIterator) Jobs() []domain.ExternalOpportunity       { return it.jobs }
func (it *sitemapIterator) RawPayload() []byte                { return it.raw }
func (it *sitemapIterator) HTTPStatus() int                   { return 200 }
func (it *sitemapIterator) Err() error                        { return nil }
func (it *sitemapIterator) Cursor() json.RawMessage           { return nil }
func (it *sitemapIterator) Content() *content.Extracted       { return nil }
