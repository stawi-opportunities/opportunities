// Package greenhouse implements a Connector for Greenhouse job boards.
package greenhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	connectors "github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// Connector crawls Greenhouse-hosted job boards.
type Connector struct{ client *httpx.Client }

// New creates a Greenhouse Connector using the provided HTTP client.
func New(client *httpx.Client) *Connector { return &Connector{client: client} }

// Type returns the SourceType this connector handles.
func (c *Connector) Type() domain.SourceType { return domain.SourceGreenhouse }

// Crawl fetches all jobs from the Greenhouse boards API for the given source.
func (c *Connector) Crawl(ctx context.Context, src domain.Source) connectors.CrawlIterator {
	company := strings.Trim(strings.TrimPrefix(src.BaseURL, "https://boards.greenhouse.io/"), "/")
	u := fmt.Sprintf("https://boards-api.greenhouse.io/v1/boards/%s/jobs?content=true", company)

	body, status, err := c.client.Get(ctx, u, nil)
	if err != nil {
		return connectors.NewSinglePageIterator(nil, body, status, err)
	}

	var payload struct {
		Jobs []struct {
			ID       int64  `json:"id"`
			Title    string `json:"title"`
			Absolute string `json:"absolute_url"`
			Location struct {
				Name string `json:"name"`
			} `json:"location"`
			Content string `json:"content"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return connectors.NewSinglePageIterator(nil, body, status, fmt.Errorf("decode greenhouse payload: %w", err))
	}

	out := make([]domain.ExternalJob, 0, len(payload.Jobs))
	for _, j := range payload.Jobs {
		out = append(out, domain.ExternalJob{
			ExternalID:   fmt.Sprintf("%d", j.ID),
			SourceURL:    src.BaseURL,
			ApplyURL:     j.Absolute,
			Title:        j.Title,
			Company:      company,
			LocationText: j.Location.Name,
			Description:  j.Content,
		})
	}
	return connectors.NewSinglePageIterator(out, body, status, nil)
}
