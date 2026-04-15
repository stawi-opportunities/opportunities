package generichtml

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"stawi.jobs/internal/connectors/httpx"
	"stawi.jobs/internal/domain"
)

type Connector struct{ client *httpx.Client }

func New(client *httpx.Client) *Connector    { return &Connector{client: client} }
func (c *Connector) Type() domain.SourceType { return domain.SourceGenericHTML }

var titleExpr = regexp.MustCompile(`(?is)<title>(.*?)</title>`)

func (c *Connector) Crawl(ctx context.Context, src domain.Source) ([]domain.ExternalJob, []byte, int, error) {
	body, status, err := c.client.Get(ctx, src.BaseURL, nil)
	if err != nil {
		return nil, body, status, err
	}
	lower := strings.ToLower(string(body))
	if !strings.Contains(lower, "job") && !strings.Contains(lower, "career") && !strings.Contains(lower, "opening") {
		return nil, body, status, fmt.Errorf("page does not look like job board")
	}
	title := "Job listing"
	if m := titleExpr.FindSubmatch(body); len(m) > 1 {
		title = strings.TrimSpace(string(m[1]))
	}
	jobs := []domain.ExternalJob{{ExternalID: src.BaseURL, SourceURL: src.BaseURL, ApplyURL: src.BaseURL, Title: title, Company: src.BaseURL, Description: truncate(string(body), 2000)}}
	return jobs, body, status, nil
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max]
}
