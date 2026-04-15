package adzuna

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
func (c *Connector) Type() domain.SourceType { return domain.SourceAdzuna }

func (c *Connector) Crawl(ctx context.Context, src domain.Source) ([]domain.ExternalJob, []byte, int, error) {
	u := fmt.Sprintf("https://api.adzuna.com/v1/api/jobs/%s/search/1?app_id=%s&app_key=%s&results_per_page=50&content-type=application/json", url.QueryEscape(src.Country), url.QueryEscape(c.cfg.AdzunaAppID), url.QueryEscape(c.cfg.AdzunaAppKey))
	body, status, err := c.client.Get(ctx, u, nil)
	if err != nil {
		return nil, body, status, err
	}
	var payload struct {
		Results []struct {
			ID          string `json:"id"`
			RedirectURL string `json:"redirect_url"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Created     string `json:"created"`
			Company     struct {
				DisplayName string `json:"display_name"`
			} `json:"company"`
			Location struct {
				DisplayName string `json:"display_name"`
			} `json:"location"`
			SalaryMin float64 `json:"salary_min"`
			SalaryMax float64 `json:"salary_max"`
		}
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, body, status, fmt.Errorf("decode adzuna payload: %w", err)
	}
	out := make([]domain.ExternalJob, 0, len(payload.Results))
	for _, r := range payload.Results {
		out = append(out, domain.ExternalJob{
			ExternalID:   r.ID,
			SourceURL:    src.BaseURL,
			ApplyURL:     r.RedirectURL,
			Title:        r.Title,
			Company:      r.Company.DisplayName,
			LocationText: r.Location.DisplayName,
			SalaryMin:    r.SalaryMin,
			SalaryMax:    r.SalaryMax,
			Currency:     "USD",
			Description:  r.Description,
		})
	}
	return out, body, status, nil
}
