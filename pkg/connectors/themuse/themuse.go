// Package themuse implements a connector for The Muse public jobs API.
package themuse

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"stawi.jobs/pkg/connectors"
	"stawi.jobs/pkg/connectors/httpx"
	"stawi.jobs/pkg/content"
	"stawi.jobs/pkg/domain"
)

const (
	baseURL  = "https://www.themuse.com/api/public/jobs"
	maxPages = 50
)

// Connector fetches jobs from The Muse public jobs API.
type Connector struct {
	client *httpx.Client
}

// New creates a Muse Connector with sensible defaults.
func New() *Connector {
	return &Connector{
		client: httpx.NewClient(30*time.Second, "stawi.jobs/crawler"),
	}
}

// Type returns the SourceType for The Muse.
func (c *Connector) Type() domain.SourceType { return domain.SourceTheMuse }

// Crawl returns a paginated CrawlIterator over The Muse job listings.
func (c *Connector) Crawl(_ context.Context, _ domain.Source) connectors.CrawlIterator {
	return &iterator{client: c.client, page: 0}
}

// iterator implements CrawlIterator for paginated Muse responses.
type iterator struct {
	client     *httpx.Client
	page       int
	pageCount  int
	jobs       []domain.ExternalJob
	raw        []byte
	httpStatus int
	err        error
	done       bool
	started    bool
}

type museJob struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Company struct {
		Name string `json:"name"`
	} `json:"company"`
	Locations []struct {
		Name string `json:"name"`
	} `json:"locations"`
	Contents string `json:"contents"`
	Refs     struct {
		LandingPage string `json:"landing_page"`
	} `json:"refs"`
	Type string `json:"type"`
}

type museResponse struct {
	Results   []museJob `json:"results"`
	PageCount int       `json:"page_count"`
}

func (it *iterator) Next(ctx context.Context) bool {
	if it.done || it.err != nil {
		return false
	}

	// After first page, check termination conditions.
	if it.started {
		if it.page >= it.pageCount || it.page >= maxPages {
			it.done = true
			return false
		}
	}

	url := fmt.Sprintf("%s?page=%d", baseURL, it.page)
	raw, status, err := it.client.Get(ctx, url, map[string]string{
		"Accept": "application/json",
	})
	it.raw = raw
	it.httpStatus = status
	it.started = true

	if err != nil {
		it.err = err
		it.done = true
		return false
	}
	if status != 200 {
		it.err = fmt.Errorf("themuse: unexpected status %d", status)
		it.done = true
		return false
	}

	var resp museResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		it.err = fmt.Errorf("themuse: unmarshal: %w", err)
		it.done = true
		return false
	}

	if len(resp.Results) == 0 {
		it.done = true
		return false
	}

	it.pageCount = resp.PageCount

	jobs := make([]domain.ExternalJob, 0, len(resp.Results))
	for _, item := range resp.Results {
		location := ""
		if len(item.Locations) > 0 {
			location = item.Locations[0].Name
		}
		jobs = append(jobs, domain.ExternalJob{
			ExternalID:     strconv.Itoa(item.ID),
			Title:          item.Name,
			Company:        item.Company.Name,
			LocationText:   location,
			Description:    item.Contents,
			ApplyURL:       item.Refs.LandingPage,
			EmploymentType: item.Type,
		})
	}
	it.jobs = jobs
	it.page++
	return true
}

func (it *iterator) Jobs() []domain.ExternalJob    { return it.jobs }
func (it *iterator) RawPayload() []byte            { return it.raw }
func (it *iterator) HTTPStatus() int               { return it.httpStatus }
func (it *iterator) Err() error                    { return it.err }
func (it *iterator) Cursor() json.RawMessage       { return nil }
func (it *iterator) Content() *content.Extracted   { return nil }
