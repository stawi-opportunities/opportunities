// Package jobicy implements a connector for the Jobicy remote jobs API.
package jobicy

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

const apiURL = "https://jobicy.com/api/v2/remote-jobs?count=50"

// Connector fetches jobs from the Jobicy remote jobs JSON API.
type Connector struct {
	client *httpx.Client
}

// New creates a Jobicy Connector with sensible defaults.
func New() *Connector {
	return &Connector{
		client: httpx.NewClient(30*time.Second, "github.com/stawi-opportunities/opportunities/crawler"),
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
		ID             int                   `json:"id"`
		URL            string                `json:"url"`
		JobTitle       string                `json:"jobTitle"`
		CompanyName    string                `json:"companyName"`
		JobGeo         string                `json:"jobGeo"`
		JobType        connectors.FlexString `json:"jobType"`
		JobExcerpt     string                `json:"jobExcerpt"`
		SalaryCurrency string                `json:"salaryCurrency"`
	}

	type jobicyResponse struct {
		Jobs []jobicyJob `json:"jobs"`
	}

	var resp jobicyResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return connectors.NewSinglePageIterator(nil, raw, status,
			fmt.Errorf("jobicy: unmarshal: %w", err))
	}

	jobs := make([]domain.ExternalOpportunity, 0, len(resp.Jobs))
	for _, item := range resp.Jobs {
		jobs = append(jobs, domain.ExternalOpportunity{
			Kind:          "job",
			ExternalID:    strconv.Itoa(item.ID),
			ApplyURL:      item.URL,
			Title:         item.JobTitle,
			IssuingEntity: item.CompanyName,
			LocationText:  item.JobGeo,
			Description:   item.JobExcerpt,
			Currency:      item.SalaryCurrency,
			Remote:        true,
			Attributes: map[string]any{
				"employment_type": item.JobType.String(),
				"remote_type":     "remote",
			},
		})
	}

	return connectors.NewSinglePageIterator(jobs, raw, status, nil)
}
