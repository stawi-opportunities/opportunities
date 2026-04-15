// Package lever implements a Connector for Lever job boards.
package lever

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	connectors "stawi.jobs/pkg/connectors"
	"stawi.jobs/pkg/connectors/httpx"
	"stawi.jobs/pkg/domain"
)

// Connector crawls Lever-hosted job boards.
type Connector struct{ client *httpx.Client }

// New creates a Lever Connector using the provided HTTP client.
func New(client *httpx.Client) *Connector { return &Connector{client: client} }

// Type returns the SourceType this connector handles.
func (c *Connector) Type() domain.SourceType { return domain.SourceLever }

// Crawl fetches all postings from the Lever API for the given source.
func (c *Connector) Crawl(ctx context.Context, src domain.Source) connectors.CrawlIterator {
	clientName := strings.Trim(strings.TrimPrefix(src.BaseURL, "https://jobs.lever.co/"), "/")
	u := fmt.Sprintf("https://api.lever.co/v0/postings/%s?mode=json", clientName)

	body, status, err := c.client.Get(ctx, u, nil)
	if err != nil {
		return connectors.NewSinglePageIterator(nil, body, status, err)
	}

	var payload []struct {
		ID        string `json:"id"`
		Text      string `json:"text"`
		HostedURL string `json:"hostedUrl"`
		Categories struct {
			Location   string `json:"location"`
			Commitment string `json:"commitment"`
		} `json:"categories"`
		DescriptionPlain string `json:"descriptionPlain"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return connectors.NewSinglePageIterator(nil, body, status, fmt.Errorf("decode lever payload: %w", err))
	}

	out := make([]domain.ExternalJob, 0, len(payload))
	for _, j := range payload {
		out = append(out, domain.ExternalJob{
			ExternalID:     j.ID,
			SourceURL:      src.BaseURL,
			ApplyURL:       j.HostedURL,
			Title:          j.Text,
			Company:        clientName,
			LocationText:   j.Categories.Location,
			EmploymentType: j.Categories.Commitment,
			Description:    j.DescriptionPlain,
		})
	}
	return connectors.NewSinglePageIterator(out, body, status, nil)
}
