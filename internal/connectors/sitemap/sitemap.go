package sitemap

import (
	"context"
	"encoding/xml"
	"fmt"
	"strings"

	"stawi.jobs/internal/connectors/httpx"
	"stawi.jobs/internal/domain"
)

type Connector struct{ client *httpx.Client }

func New(client *httpx.Client) *Connector    { return &Connector{client: client} }
func (c *Connector) Type() domain.SourceType { return domain.SourceSitemap }

func (c *Connector) Crawl(ctx context.Context, src domain.Source) ([]domain.ExternalJob, []byte, int, error) {
	u := strings.TrimSuffix(src.BaseURL, "/") + "/sitemap.xml"
	body, status, err := c.client.Get(ctx, u, nil)
	if err != nil {
		return nil, body, status, err
	}
	var payload struct {
		URLs []struct {
			Loc string `xml:"loc"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal(body, &payload); err != nil {
		return nil, body, status, fmt.Errorf("decode sitemap: %w", err)
	}
	out := make([]domain.ExternalJob, 0)
	for _, u := range payload.URLs {
		if strings.Contains(strings.ToLower(u.Loc), "job") || strings.Contains(strings.ToLower(u.Loc), "career") {
			out = append(out, domain.ExternalJob{ExternalID: u.Loc, SourceURL: src.BaseURL, ApplyURL: u.Loc, Title: "Job Opportunity", Company: src.BaseURL})
		}
	}
	if len(out) == 0 {
		return nil, body, status, fmt.Errorf("no job urls found in sitemap")
	}
	return out, body, status, nil
}
