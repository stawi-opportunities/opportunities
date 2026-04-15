// Package smartrecruiters implements a Connector for SmartRecruiters job boards.
package smartrecruiters

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	connectors "stawi.jobs/pkg/connectors"
	"stawi.jobs/pkg/connectors/httpx"
	"stawi.jobs/pkg/domain"
)

// Connector crawls SmartRecruiters-hosted job boards.
type Connector struct{ client *httpx.Client }

// New creates a SmartRecruiters Connector using the provided HTTP client.
func New(client *httpx.Client) *Connector { return &Connector{client: client} }

// Type returns the SourceType this connector handles.
func (c *Connector) Type() domain.SourceType { return domain.SourceSmartRecruitersAPI }

// Crawl fetches all postings from the SmartRecruiters API for the given source.
func (c *Connector) Crawl(ctx context.Context, src domain.Source) connectors.CrawlIterator {
	base := strings.TrimSuffix(src.BaseURL, "/")
	u := base + "/postings"

	body, status, err := c.client.Get(ctx, u, nil)
	if err != nil {
		return connectors.NewSinglePageIterator(nil, body, status, err)
	}

	var payload struct {
		Content []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Ref      string `json:"ref"`
			Location struct {
				City    string `json:"city"`
				Country string `json:"country"`
			} `json:"location"`
			Department struct {
				Label string `json:"label"`
			} `json:"department"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return connectors.NewSinglePageIterator(nil, body, status, fmt.Errorf("decode smartrecruiters payload: %w", err))
	}

	out := make([]domain.ExternalJob, 0, len(payload.Content))
	for _, job := range payload.Content {
		out = append(out, domain.ExternalJob{
			ExternalID:   job.ID,
			SourceURL:    src.BaseURL,
			ApplyURL:     src.BaseURL + "/" + job.ID,
			Title:        job.Name,
			Company:      job.Department.Label,
			LocationText: strings.TrimSpace(job.Location.City + ", " + job.Location.Country),
		})
	}
	return connectors.NewSinglePageIterator(out, body, status, nil)
}
