package serpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"stawi.jobs/internal/config"
	"stawi.jobs/internal/connectors/httpx"
	"stawi.jobs/internal/domain"
)

type Connector struct {
	client *httpx.Client
	cfg    config.Config
}

func New(cfg config.Config, client *httpx.Client) *Connector {
	return &Connector{client: client, cfg: cfg}
}
func (c *Connector) Type() domain.SourceType { return domain.SourceSerpAPI }

func (c *Connector) Crawl(ctx context.Context, src domain.Source) ([]domain.ExternalJob, []byte, int, error) {
	q := "software engineer"
	u := fmt.Sprintf("https://serpapi.com/search.json?engine=google_jobs&q=%s&api_key=%s&hl=en&gl=%s", url.QueryEscape(q), url.QueryEscape(c.cfg.SerpAPIKey), url.QueryEscape(src.Country))
	body, status, err := c.client.Get(ctx, u, nil)
	if err != nil {
		return nil, body, status, err
	}
	var payload struct {
		Jobs []struct {
			JobID       string `json:"job_id"`
			Title       string `json:"title"`
			Company     string `json:"company_name"`
			Location    string `json:"location"`
			Description string `json:"description"`
			ApplyOpts   []struct {
				Link string `json:"link"`
			} `json:"apply_options"`
		} `json:"jobs_results"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, body, status, fmt.Errorf("decode serpapi payload: %w", err)
	}
	out := make([]domain.ExternalJob, 0, len(payload.Jobs))
	for _, j := range payload.Jobs {
		apply := ""
		if len(j.ApplyOpts) > 0 {
			apply = j.ApplyOpts[0].Link
		}
		out = append(out, domain.ExternalJob{ExternalID: j.JobID, SourceURL: src.BaseURL, ApplyURL: apply, Title: j.Title, Company: j.Company, LocationText: j.Location, Description: j.Description})
	}
	return out, body, status, nil
}
