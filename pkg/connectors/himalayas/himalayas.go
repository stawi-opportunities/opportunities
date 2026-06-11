// Package himalayas implements a connector for the Himalayas remote jobs API.
package himalayas

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/content"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// baseURL is a var so tests can point the connector at a stub server.
var baseURL = "https://himalayas.app/jobs/api"

// Connector fetches jobs from the Himalayas remote jobs API.
type Connector struct {
	client *httpx.Client
}

// New creates a Himalayas Connector with sensible defaults.
func New() *Connector {
	return &Connector{
		client: httpx.NewClient(30*time.Second, "github.com/stawi-opportunities/opportunities/crawler"),
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
	jobs       []domain.ExternalOpportunity
	raw        []byte
	httpStatus int
	err        error
	done       bool
}

type himalayasJob struct {
	ID                   interface{}           `json:"id"`
	Title                string                `json:"title"`
	CompanyName          string                `json:"companyName"`
	LocationRestrictions connectors.FlexString `json:"locationRestrictions"`
	Excerpt              string                `json:"excerpt"`
	ApplicationLink      string                `json:"applicationLink"`
	ExternalURL          string                `json:"externalUrl"`
	Currency             string                `json:"currency"`
	MinSalary            float64               `json:"minSalary"`
	MaxSalary            float64               `json:"maxSalary"`
	EmploymentType       string                `json:"employmentType"`
	Seniority            connectors.FlexString `json:"seniority"`
}

type himalayasResponse struct {
	Jobs []himalayasJob `json:"jobs"`
}

// pageDelay paces the pagination loop. The board is deep (1700+ pages
// at limit=50) and an undelayed loop trips the API's rate limiter
// partway through: every prod crawl ended in "all 5 attempts failed:
// retryable HTTP status 429" after storing tens of thousands of jobs,
// marking an overwhelmingly-successful crawl as failed.
var pageDelay = 400 * time.Millisecond

func (it *iterator) Next(ctx context.Context) bool {
	if it.done || it.err != nil {
		return false
	}

	if it.page > 1 {
		select {
		case <-ctx.Done():
			it.err = ctx.Err()
			it.done = true
			return false
		case <-time.After(pageDelay):
		}
	}

	url := fmt.Sprintf("%s?page=%d&limit=50", baseURL, it.page)
	raw, status, err := it.client.Get(ctx, url, map[string]string{
		"Accept": "application/json",
	})
	it.raw = raw
	it.httpStatus = status

	// A 429 that persists through httpx's retry budget DEEP into
	// pagination means "you have enough for today", not "the source is
	// broken" — end the crawl cleanly and let the next scheduled run
	// pick up fresh postings. Only page 1 treats it as a real failure.
	if it.page > 1 && (status == 429 || (err != nil && strings.Contains(err.Error(), "status 429"))) {
		it.done = true
		return false
	}
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

	jobs := make([]domain.ExternalOpportunity, 0, len(resp.Jobs))
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

		jobs = append(jobs, domain.ExternalOpportunity{
			Kind:          "job",
			ExternalID:    externalID,
			Title:         item.Title,
			IssuingEntity: item.CompanyName,
			LocationText:  item.LocationRestrictions.String(),
			Description:   item.Excerpt,
			ApplyURL:      applyURL,
			Currency:      item.Currency,
			AmountMin:     item.MinSalary,
			AmountMax:     item.MaxSalary,
			Remote:        true,
			Attributes: map[string]any{
				"employment_type": item.EmploymentType,
				"remote_type":     "remote",
			},
		})
	}
	it.jobs = jobs
	it.page++
	return true
}

func (it *iterator) Items() []domain.ExternalOpportunity { return it.jobs }
func (it *iterator) RawPayload() []byte                  { return it.raw }
func (it *iterator) HTTPStatus() int                     { return it.httpStatus }
func (it *iterator) Err() error                          { return it.err }
func (it *iterator) Cursor() json.RawMessage             { return nil }
func (it *iterator) Content() *content.Extracted         { return nil }
