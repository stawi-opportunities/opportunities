// Package findwork implements a connector for the Findwork.dev jobs API.
package findwork

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"stawi.jobs/pkg/connectors"
	"stawi.jobs/pkg/connectors/httpx"
	"stawi.jobs/pkg/domain"
)

const baseURL = "https://findwork.dev/api/jobs/"

// Connector fetches jobs from the Findwork.dev public jobs API.
type Connector struct {
	client *httpx.Client
}

// New creates a Findwork Connector with sensible defaults.
func New() *Connector {
	return &Connector{
		client: httpx.NewClient(30*time.Second, "stawi.jobs/crawler"),
	}
}

// Type returns the SourceType for Findwork.
func (c *Connector) Type() domain.SourceType { return domain.SourceFindwork }

// Crawl returns a paginated CrawlIterator over Findwork job listings.
func (c *Connector) Crawl(_ context.Context, _ domain.Source) connectors.CrawlIterator {
	return &iterator{client: c.client, page: 1}
}

// iterator implements CrawlIterator for paginated Findwork responses.
type iterator struct {
	client     *httpx.Client
	page       int
	jobs       []domain.ExternalJob
	raw        []byte
	httpStatus int
	err        error
	done       bool
}

type findworkJob struct {
	ID          int    `json:"id"`
	Role        string `json:"role"`
	CompanyName string `json:"company_name"`
	Location    string `json:"location"`
	Text        string `json:"text"`
	URL         string `json:"url"`
	Remote      bool   `json:"remote"`
}

type findworkResponse struct {
	Results []findworkJob `json:"results"`
	Next    string        `json:"next"`
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
		it.err = fmt.Errorf("findwork: unexpected status %d", status)
		it.done = true
		return false
	}

	var resp findworkResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		it.err = fmt.Errorf("findwork: unmarshal: %w", err)
		it.done = true
		return false
	}

	if len(resp.Results) == 0 || resp.Next == "" {
		it.done = true
		if len(resp.Results) == 0 {
			return false
		}
	}

	jobs := make([]domain.ExternalJob, 0, len(resp.Results))
	for _, item := range resp.Results {
		remoteType := ""
		if item.Remote {
			remoteType = "remote"
		}
		jobs = append(jobs, domain.ExternalJob{
			ExternalID:   strconv.Itoa(item.ID),
			Title:        item.Role,
			Company:      item.CompanyName,
			LocationText: item.Location,
			Description:  item.Text,
			ApplyURL:     item.URL,
			RemoteType:   remoteType,
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
