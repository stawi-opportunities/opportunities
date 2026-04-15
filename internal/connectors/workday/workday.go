package workday

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
func (c *Connector) Type() domain.SourceType { return domain.SourceWorkday }

func (c *Connector) Crawl(ctx context.Context, src domain.Source) ([]domain.ExternalJob, []byte, int, error) {
	base := strings.TrimSuffix(src.BaseURL, "/")
	u := base + "/wday/cxs/jobs"
	body, status, err := c.client.Get(ctx, u, nil)
	if err != nil {
		return nil, body, status, err
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
		return nil, body, status, fmt.Errorf("decode workday payload: %w", err)
	}
	out := make([]domain.ExternalJob, 0, len(payload.JobPostings))
	for _, j := range payload.JobPostings {
		d := strings.Join(j.BulletFields, " ")
		out = append(out, domain.ExternalJob{ExternalID: j.ID, SourceURL: src.BaseURL, ApplyURL: base + "/job/" + j.ExternalPath, Title: j.Title, Company: base, LocationText: j.LocationsText, Description: d})
	}
	return out, body, status, nil
}
