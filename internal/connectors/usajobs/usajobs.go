package usajobs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
func (c *Connector) Type() domain.SourceType { return domain.SourceUSAJobs }

func (c *Connector) Crawl(ctx context.Context, src domain.Source) ([]domain.ExternalJob, []byte, int, error) {
	u := fmt.Sprintf("https://data.usajobs.gov/api/search?ResultsPerPage=100&Keyword=%s", url.QueryEscape("software"))
	headers := map[string]string{
		http.CanonicalHeaderKey("Host"):              "data.usajobs.gov",
		http.CanonicalHeaderKey("User-Agent"):        c.cfg.USAJobsEmail,
		http.CanonicalHeaderKey("Authorization-Key"): c.cfg.USAJobsKey,
	}
	body, status, err := c.client.Get(ctx, u, headers)
	if err != nil {
		return nil, body, status, err
	}
	var payload struct {
		SearchResult struct {
			Items []struct {
				MatchedObjectDescriptor struct {
					PositionID              string `json:"PositionID"`
					PositionTitle           string `json:"PositionTitle"`
					OrganizationName        string `json:"OrganizationName"`
					PositionLocationDisplay string `json:"PositionLocationDisplay"`
					PositionURI             string `json:"PositionURI"`
					QualificationSummary    string `json:"QualificationSummary"`
				} `json:"MatchedObjectDescriptor"`
			} `json:"SearchResultItems"`
		} `json:"SearchResult"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, body, status, fmt.Errorf("decode usajobs payload: %w", err)
	}
	out := make([]domain.ExternalJob, 0, len(payload.SearchResult.Items))
	for _, item := range payload.SearchResult.Items {
		m := item.MatchedObjectDescriptor
		out = append(out, domain.ExternalJob{ExternalID: m.PositionID, SourceURL: src.BaseURL, ApplyURL: m.PositionURI, Title: m.PositionTitle, Company: m.OrganizationName, LocationText: m.PositionLocationDisplay, Description: m.QualificationSummary})
	}
	return out, body, status, nil
}
