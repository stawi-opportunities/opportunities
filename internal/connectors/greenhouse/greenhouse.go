package greenhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"stawi.jobs/internal/connectors/httpx"
	"stawi.jobs/internal/domain"
)

type Connector struct{ client *httpx.Client }

func New(client *httpx.Client) *Connector    { return &Connector{client: client} }
func (c *Connector) Type() domain.SourceType { return domain.SourceGreenhouse }

func (c *Connector) Crawl(ctx context.Context, src domain.Source) ([]domain.ExternalJob, []byte, int, error) {
	company := strings.Trim(strings.TrimPrefix(src.BaseURL, "https://boards.greenhouse.io/"), "/")
	u := fmt.Sprintf("https://boards-api.greenhouse.io/v1/boards/%s/jobs", company)
	body, status, err := c.client.Get(ctx, u, nil)
	if err != nil {
		return nil, body, status, err
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
		return nil, body, status, fmt.Errorf("decode greenhouse payload: %w", err)
	}
	out := make([]domain.ExternalJob, 0, len(payload.Jobs))
	for _, j := range payload.Jobs {
		out = append(out, domain.ExternalJob{ExternalID: fmt.Sprintf("%d", j.ID), SourceURL: src.BaseURL, ApplyURL: j.Absolute, Title: j.Title, Company: company, LocationText: j.Location.Name, Description: j.Content})
	}
	return out, body, status, nil
}
