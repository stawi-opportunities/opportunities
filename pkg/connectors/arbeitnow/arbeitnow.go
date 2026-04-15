// Package arbeitnow implements a connector for the Arbeitnow job board API.
package arbeitnow

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"stawi.jobs/pkg/connectors"
	"stawi.jobs/pkg/connectors/httpx"
	"stawi.jobs/pkg/domain"
)

const baseURL = "https://www.arbeitnow.com/api/job-board-api"

// Connector fetches jobs from the Arbeitnow public JSON API.
type Connector struct {
	client *httpx.Client
}

// New creates an Arbeitnow Connector with sensible defaults.
func New() *Connector {
	return &Connector{
		client: httpx.NewClient(30*time.Second, "stawi.jobs/crawler"),
	}
}

// Type returns the SourceType for Arbeitnow.
func (c *Connector) Type() domain.SourceType { return domain.SourceArbeitnow }

// Crawl returns a paginated CrawlIterator over Arbeitnow job listings.
func (c *Connector) Crawl(_ context.Context, _ domain.Source) connectors.CrawlIterator {
	return &iterator{client: c.client, page: 1}
}

// iterator implements CrawlIterator for paginated Arbeitnow responses.
type iterator struct {
	client     *httpx.Client
	page       int
	jobs       []domain.ExternalJob
	raw        []byte
	httpStatus int
	err        error
	done       bool
}

type arbeitnowJob struct {
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	CompanyName string   `json:"company_name"`
	Location    string   `json:"location"`
	Description string   `json:"description"`
	URL         string   `json:"url"`
	Remote      bool     `json:"remote"`
	Tags        []string `json:"tags"`
}

type arbeitnowResponse struct {
	Data  []arbeitnowJob `json:"data"`
	Links struct {
		Next string `json:"next"`
	} `json:"links"`
}

func (it *iterator) Next(ctx context.Context) bool {
	if it.done || it.err != nil {
		return false
	}

	url := fmt.Sprintf("%s?page=%d", baseURL, it.page)
	raw, status, err := it.client.Get(ctx, url, map[string]string{
		"Accept": "application/json",
	})
	it.raw = raw
	it.httpStatus = status
	if err != nil {
		it.err = err
		it.done = true
		return false
	}
	if status != 200 {
		it.err = fmt.Errorf("arbeitnow: unexpected status %d", status)
		it.done = true
		return false
	}

	var resp arbeitnowResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		it.err = fmt.Errorf("arbeitnow: unmarshal: %w", err)
		it.done = true
		return false
	}

	if len(resp.Data) == 0 || resp.Links.Next == "" {
		it.done = true
		// Still return true if there are jobs on this last page.
		if len(resp.Data) == 0 {
			return false
		}
	}

	jobs := make([]domain.ExternalJob, 0, len(resp.Data))
	for _, item := range resp.Data {
		remoteType := ""
		if item.Remote {
			remoteType = "remote"
		}
		jobs = append(jobs, domain.ExternalJob{
			ExternalID:  item.Slug,
			Title:       item.Title,
			Company:     item.CompanyName,
			LocationText: item.Location,
			Description: item.Description,
			ApplyURL:    item.URL,
			RemoteType:  remoteType,
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
