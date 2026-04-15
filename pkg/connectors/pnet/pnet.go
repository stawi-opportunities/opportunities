// Package pnet implements a connector for the PNet job board.
package pnet

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"stawi.jobs/pkg/connectors"
	"stawi.jobs/pkg/connectors/httpx"
	"stawi.jobs/pkg/domain"
)

const maxPages = 100

var (
	jobLinkRe  = regexp.MustCompile(`href=["'](/job[s]?/[^"']+)["']`)
	titleTagRe = regexp.MustCompile(`(?i)<title[^>]*>(.*?)</title>`)
	h1Re       = regexp.MustCompile(`(?i)<h1[^>]*>(.*?)</h1>`)
	companyRe  = regexp.MustCompile(`(?i)class="[^"]*company[^"]*"[^>]*>(.*?)<`)
	locationRe = regexp.MustCompile(`(?i)class="[^"]*location[^"]*"[^>]*>(.*?)<`)
	descRe     = regexp.MustCompile(`(?is)class="[^"]*description[^"]*"[^>]*>(.*?)</div>`)
	tagRe      = regexp.MustCompile(`<[^>]+>`)
)

func extractFirst(re *regexp.Regexp, html string) string {
	m := re.FindStringSubmatch(html)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func extractTitle(html string) string {
	if t := extractFirst(h1Re, html); t != "" {
		return stripTags(t)
	}
	return stripTags(extractFirst(titleTagRe, html))
}

func stripTags(s string) string {
	return strings.TrimSpace(tagRe.ReplaceAllString(s, ""))
}

// Connector fetches jobs from PNet.
type Connector struct {
	client *httpx.Client
}

// New creates a PNet Connector.
func New() *Connector {
	return &Connector{
		client: httpx.NewClient(30*time.Second, "stawi.jobs/crawler"),
	}
}

// Type returns the SourceType for PNet.
func (c *Connector) Type() domain.SourceType { return domain.SourcePNet }

// Crawl starts a multi-page crawl and returns a CrawlIterator.
func (c *Connector) Crawl(_ context.Context, source domain.Source) connectors.CrawlIterator {
	return &iterator{
		client:  c.client,
		baseURL: source.BaseURL,
		page:    1,
	}
}

type iterator struct {
	client  *httpx.Client
	baseURL string
	page    int
	jobs    []domain.ExternalJob
	raw     []byte
	status  int
	err     error
}

func (it *iterator) Next(ctx context.Context) bool {
	if it.err != nil || it.page > maxPages {
		return false
	}

	listURL := fmt.Sprintf("%s/jobs?page=%d", it.baseURL, it.page)
	raw, status, err := it.client.Get(ctx, listURL, nil)
	it.raw = raw
	it.status = status
	if err != nil {
		log.Printf("pnet: fetch page %d: %v", it.page, err)
		it.err = err
		return false
	}
	if status != 200 {
		log.Printf("pnet: page %d status %d", it.page, status)
		it.err = fmt.Errorf("pnet: status %d on page %d", status, it.page)
		return false
	}

	html := string(raw)
	matches := jobLinkRe.FindAllStringSubmatch(html, -1)
	if len(matches) == 0 {
		return false
	}

	seen := make(map[string]struct{})
	var links []string
	for _, m := range matches {
		path := m[1]
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		links = append(links, it.baseURL+path)
	}

	var jobs []domain.ExternalJob
	for _, link := range links {
		job, err := fetchDetail(ctx, it.client, link)
		if err != nil {
			log.Printf("pnet: detail %s: %v", link, err)
			continue
		}
		jobs = append(jobs, job)
	}

	it.jobs = jobs
	it.page++
	return true
}

func fetchDetail(ctx context.Context, client *httpx.Client, url string) (domain.ExternalJob, error) {
	raw, status, err := client.Get(ctx, url, nil)
	if err != nil {
		return domain.ExternalJob{}, err
	}
	if status != 200 {
		return domain.ExternalJob{}, fmt.Errorf("status %d", status)
	}
	html := string(raw)

	title := extractTitle(html)
	company := stripTags(extractFirst(companyRe, html))
	location := stripTags(extractFirst(locationRe, html))
	description := stripTags(extractFirst(descRe, html))

	return domain.ExternalJob{
		ExternalID:   url,
		SourceURL:    url,
		ApplyURL:     url,
		Title:        title,
		Company:      company,
		LocationText: location,
		Description:  description,
	}, nil
}

func (it *iterator) Jobs() []domain.ExternalJob { return it.jobs }
func (it *iterator) RawPayload() []byte          { return it.raw }
func (it *iterator) HTTPStatus() int             { return it.status }
func (it *iterator) Err() error                  { return it.err }
func (it *iterator) Cursor() json.RawMessage     { return nil }
