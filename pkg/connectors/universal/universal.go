// Package universal provides an AI-powered connector that discovers job links
// on any HTML listing page using an LLM, replacing per-site regex parsers.
package universal

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"

	"stawi.jobs/pkg/connectors"
	"stawi.jobs/pkg/connectors/httpx"
	"stawi.jobs/pkg/content"
	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/extraction"
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

// Crawl starts an AI-driven crawl of the source's listing page(s).
func (c *Connector) Crawl(_ context.Context, source domain.Source) connectors.CrawlIterator {
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
	jobs      []domain.ExternalJob
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
		log.Printf("universal: fetch page %d (%s): %v", it.page+1, it.nextURL, err)
		it.err = err
		return false
	}
	if status != 200 {
		log.Printf("universal: page %d status %d (%s)", it.page+1, status, it.nextURL)
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
		log.Printf("universal: AI discover links failed for %s: %v", it.nextURL, err)
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
	var jobs []domain.ExternalJob
	for _, link := range jobLinks {
		if _, ok := seen[link]; ok {
			continue
		}
		seen[link] = struct{}{}
		jobs = append(jobs, domain.ExternalJob{
			ExternalID: link,
			ApplyURL:   link,
		})
	}

	it.jobs = jobs
	it.page++
	return true
}

func (it *iterator) Jobs() []domain.ExternalJob       { return it.jobs }
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
