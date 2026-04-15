// Package careers24 implements a connector for the Careers24 job board.
package careers24

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"time"

	"stawi.jobs/pkg/connectors"
	"stawi.jobs/pkg/connectors/httpx"
	"stawi.jobs/pkg/domain"
)

const maxPages = 100

var jobLinkRe = regexp.MustCompile(`href=["'](/jobs/[^"']+)["']`)

// Connector fetches jobs from Careers24.
type Connector struct {
	client *httpx.Client
}

// New creates a Careers24 Connector.
func New() *Connector {
	return &Connector{
		client: httpx.NewClient(30*time.Second, "stawi.jobs/crawler"),
	}
}

// Type returns the SourceType for Careers24.
func (c *Connector) Type() domain.SourceType { return domain.SourceCareers24 }

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
		log.Printf("careers24: fetch page %d: %v", it.page, err)
		it.err = err
		return false
	}
	if status != 200 {
		log.Printf("careers24: page %d status %d", it.page, status)
		it.err = fmt.Errorf("careers24: status %d on page %d", status, it.page)
		return false
	}

	html := string(raw)
	matches := jobLinkRe.FindAllStringSubmatch(html, -1)
	if len(matches) == 0 {
		return false
	}

	seen := make(map[string]struct{})
	var jobs []domain.ExternalJob
	for _, m := range matches {
		path := m[1]
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		link := it.baseURL + path
		jobs = append(jobs, domain.ExternalJob{
			ExternalID: link,
			ApplyURL:   link,
		})
	}

	it.jobs = jobs
	it.page++
	return true
}

func (it *iterator) Jobs() []domain.ExternalJob { return it.jobs }
func (it *iterator) RawPayload() []byte          { return it.raw }
func (it *iterator) HTTPStatus() int             { return it.status }
func (it *iterator) Err() error                  { return it.err }
func (it *iterator) Cursor() json.RawMessage     { return nil }
