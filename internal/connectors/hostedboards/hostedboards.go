package hostedboards

import (
	"context"
	"fmt"
	"regexp"

	"stawi.jobs/internal/connectors/httpx"
	"stawi.jobs/internal/domain"
)

type Connector struct{ client *httpx.Client }

func New(client *httpx.Client) *Connector    { return &Connector{client: client} }
func (c *Connector) Type() domain.SourceType { return domain.SourceHostedBoards }

var linkExpr = regexp.MustCompile(`(?i)href=["']([^"']*(job|career)[^"']*)["']`)

func (c *Connector) Crawl(ctx context.Context, src domain.Source) ([]domain.ExternalJob, []byte, int, error) {
	body, status, err := c.client.Get(ctx, src.BaseURL, nil)
	if err != nil {
		return nil, body, status, err
	}
	matches := linkExpr.FindAllSubmatch(body, -1)
	out := make([]domain.ExternalJob, 0, len(matches))
	for _, m := range matches {
		url := string(m[1])
		out = append(out, domain.ExternalJob{ExternalID: url, SourceURL: src.BaseURL, ApplyURL: url, Title: "Hosted board listing", Company: src.BaseURL})
	}
	if len(out) == 0 {
		return nil, body, status, fmt.Errorf("no hosted board links found")
	}
	return out, body, status, nil
}
