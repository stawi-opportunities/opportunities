// Package workday implements a Connector for Workday job boards.
package workday

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	connectors "github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// Connector crawls Workday-hosted job boards.
type Connector struct{ client *httpx.Client }

// New creates a Workday Connector using the provided HTTP client.
func New(client *httpx.Client) *Connector { return &Connector{client: client} }

// Type returns the SourceType this connector handles.
func (c *Connector) Type() domain.SourceType { return domain.SourceWorkday }

// Crawl fetches all job postings from the Workday API for the given source.
func (c *Connector) Crawl(ctx context.Context, src domain.Source) connectors.CrawlIterator {
	base := strings.TrimSuffix(src.BaseURL, "/")
	u := base + "/wday/cxs/jobs"

	body, status, err := c.client.Get(ctx, u, nil)
	if err != nil {
		return connectors.NewSinglePageIterator(nil, body, status, err)
	}

	var payload struct {
		JobPostings []struct {
			BulletFields  []string `json:"bulletFields"`
			Title         string   `json:"title"`
			ExternalPath  string   `json:"externalPath"`
			LocationsText string   `json:"locationsText"`
			ID            string   `json:"id"`
		} `json:"jobPostings"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return connectors.NewSinglePageIterator(nil, body, status, fmt.Errorf("decode workday payload: %w", err))
	}

	out := make([]domain.ExternalOpportunity, 0, len(payload.JobPostings))
	for _, j := range payload.JobPostings {
		d := strings.Join(j.BulletFields, " ")
		out = append(out, domain.ExternalOpportunity{
			Kind:          "job",
			ExternalID:    j.ID,
			SourceURL:     src.BaseURL,
			ApplyURL:      base + "/job/" + j.ExternalPath,
			Title:         j.Title,
			IssuingEntity: base,
			LocationText:  j.LocationsText,
			Description:   d,
		})
	}
	return connectors.NewSinglePageIterator(out, body, status, nil)
}
