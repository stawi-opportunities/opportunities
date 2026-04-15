package lever

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
func (c *Connector) Type() domain.SourceType { return domain.SourceLever }

func (c *Connector) Crawl(ctx context.Context, src domain.Source) ([]domain.ExternalJob, []byte, int, error) {
	clientName := strings.Trim(strings.TrimPrefix(src.BaseURL, "https://jobs.lever.co/"), "/")
	u := fmt.Sprintf("https://api.lever.co/v0/postings/%s?mode=json", clientName)
	body, status, err := c.client.Get(ctx, u, nil)
	if err != nil {
		return nil, body, status, err
	}
	var payload []struct {
		ID         string `json:"id"`
		Text       string `json:"text"`
		HostedURL  string `json:"hostedUrl"`
		Categories struct {
			Location   string `json:"location"`
			Commitment string `json:"commitment"`
		} `json:"categories"`
		DescriptionPlain string `json:"descriptionPlain"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, body, status, fmt.Errorf("decode lever payload: %w", err)
	}
	out := make([]domain.ExternalJob, 0, len(payload))
	for _, j := range payload {
		out = append(out, domain.ExternalJob{ExternalID: j.ID, SourceURL: src.BaseURL, ApplyURL: j.HostedURL, Title: j.Text, Company: clientName, LocationText: j.Categories.Location, EmploymentType: j.Categories.Commitment, Description: j.DescriptionPlain})
	}
	return out, body, status, nil
}
