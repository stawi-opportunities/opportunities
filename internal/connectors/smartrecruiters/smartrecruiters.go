package smartrecruiters

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
func (c *Connector) Type() domain.SourceType { return domain.SourceSmartRecruitersAPI }

func (c *Connector) Crawl(ctx context.Context, src domain.Source) ([]domain.ExternalJob, []byte, int, error) {
	base := strings.TrimSuffix(src.BaseURL, "/")
	u := base + "/postings"
	body, status, err := c.client.Get(ctx, u, nil)
	if err != nil {
		return nil, body, status, err
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
		return nil, body, status, fmt.Errorf("decode smartrecruiters payload: %w", err)
	}
	out := make([]domain.ExternalJob, 0, len(payload.Content))
	for _, job := range payload.Content {
		out = append(out, domain.ExternalJob{ExternalID: job.ID, SourceURL: src.BaseURL, ApplyURL: src.BaseURL + "/" + job.ID, Title: job.Name, Company: job.Department.Label, LocationText: strings.TrimSpace(job.Location.City + ", " + job.Location.Country)})
	}
	return out, body, status, nil
}
