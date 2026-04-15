// Package himalayas implements a connector for the Himalayas remote jobs API.
package himalayas

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

const baseURL = "https://himalayas.app/jobs/api"

// Connector fetches jobs from the Himalayas remote jobs API.
type Connector struct {
	client *httpx.Client
}

// New creates a Himalayas Connector with sensible defaults.
func New() *Connector {
	return &Connector{
		client: httpx.NewClient(30*time.Second, "stawi.jobs/crawler"),
	}
}

// Type returns the SourceType for Himalayas.
func (c *Connector) Type() domain.SourceType { return domain.SourceHimalayas }

// Crawl returns a paginated CrawlIterator over Himalayas job listings.
func (c *Connector) Crawl(_ context.Context, _ domain.Source) connectors.CrawlIterator {
	return &iterator{client: c.client, page: 1}
}

// iterator implements CrawlIterator for paginated Himalayas responses.
type iterator struct {
	client     *httpx.Client
	page       int
	jobs       []domain.ExternalJob
	raw        []byte
	httpStatus int
	err        error
	done       bool
}

type himalayasJob struct {
	ID              interface{} `json:"id"`
	Title           string      `json:"title"`
	CompanyName     string      `json:"companyName"`
	Location        string      `json:"location"`
	Description     string      `json:"description"`
	ApplicationLink string      `json:"applicationLink"`
	ExternalURL     string      `json:"externalUrl"`
	SalaryCurrency  string      `json:"salaryCurrency"`
	SalaryMin       float64     `json:"salaryMin"`
	SalaryMax       float64     `json:"salaryMax"`
}

type himalayasResponse struct {
	Jobs []himalayasJob `json:"jobs"`
}

func (it *iterator) Next(ctx context.Context) bool {
	if it.done || it.err != nil {
		return false
	}

	url := fmt.Sprintf("%s?page=%d&limit=50", baseURL, it.page)
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
		it.err = fmt.Errorf("himalayas: unexpected status %d", status)
		it.done = true
		return false
	}

	var resp himalayasResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		it.err = fmt.Errorf("himalayas: unmarshal: %w", err)
		it.done = true
		return false
	}

	if len(resp.Jobs) == 0 {
		it.done = true
		return false
	}

	jobs := make([]domain.ExternalJob, 0, len(resp.Jobs))
	for _, item := range resp.Jobs {
		// ID may be int or string in the API response.
		externalID := ""
		switch v := item.ID.(type) {
		case float64:
			externalID = strconv.FormatInt(int64(v), 10)
		case string:
			externalID = v
		}

		applyURL := item.ApplicationLink
		if applyURL == "" {
			applyURL = item.ExternalURL
		}

		jobs = append(jobs, domain.ExternalJob{
			ExternalID:   externalID,
			Title:        item.Title,
			Company:      item.CompanyName,
			LocationText: item.Location,
			Description:  item.Description,
			ApplyURL:     applyURL,
			Currency:     item.SalaryCurrency,
			SalaryMin:    item.SalaryMin,
			SalaryMax:    item.SalaryMax,
			RemoteType:   "remote",
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
