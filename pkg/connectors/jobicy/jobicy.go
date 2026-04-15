// Package jobicy implements a connector for the Jobicy remote jobs API.
package jobicy

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

const apiURL = "https://jobicy.com/api/v2/remote-jobs?count=50"

// Connector fetches jobs from the Jobicy remote jobs JSON API.
type Connector struct {
	client *httpx.Client
}

// New creates a Jobicy Connector with sensible defaults.
func New() *Connector {
	return &Connector{
		client: httpx.NewClient(30*time.Second, "stawi.jobs/crawler"),
	}
}

// Type returns the SourceType for Jobicy.
func (c *Connector) Type() domain.SourceType { return domain.SourceJobicy }

// Crawl fetches the single-page Jobicy API and returns a CrawlIterator.
func (c *Connector) Crawl(ctx context.Context, _ domain.Source) connectors.CrawlIterator {
	raw, status, err := c.client.Get(ctx, apiURL, map[string]string{
		"Accept": "application/json",
	})
	if err != nil {
		return connectors.NewSinglePageIterator(nil, raw, status, err)
	}
	if status != 200 {
		return connectors.NewSinglePageIterator(nil, raw, status,
			fmt.Errorf("jobicy: unexpected status %d", status))
	}

	type jobicyJob struct {
		ID             int     `json:"id"`
		URL            string  `json:"url"`
		JobTitle       string  `json:"jobTitle"`
		CompanyName    string  `json:"companyName"`
		JobGeo         string  `json:"jobGeo"`
		JobType        string  `json:"jobType"`
		JobExcerpt     string  `json:"jobExcerpt"`
		SalaryCurrency string  `json:"salaryCurrency"`
	}

	type jobicyResponse struct {
		Jobs []jobicyJob `json:"jobs"`
	}

	var resp jobicyResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return connectors.NewSinglePageIterator(nil, raw, status,
			fmt.Errorf("jobicy: unmarshal: %w", err))
	}

	jobs := make([]domain.ExternalJob, 0, len(resp.Jobs))
	for _, item := range resp.Jobs {
		jobs = append(jobs, domain.ExternalJob{
			ExternalID:     strconv.Itoa(item.ID),
			ApplyURL:       item.URL,
			Title:          item.JobTitle,
			Company:        item.CompanyName,
			LocationText:   item.JobGeo,
			EmploymentType: item.JobType,
			Description:    item.JobExcerpt,
			Currency:       item.SalaryCurrency,
			RemoteType:     "remote",
		})
	}

	return connectors.NewSinglePageIterator(jobs, raw, status, nil)
}
