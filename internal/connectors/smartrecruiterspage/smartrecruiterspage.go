package smartrecruiterspage

import (
	"context"
	"fmt"
	"regexp"

	"stawi.jobs/internal/connectors/httpx"
	"stawi.jobs/internal/domain"
)

type Connector struct{ client *httpx.Client }

func New(client *httpx.Client) *Connector    { return &Connector{client: client} }
func (c *Connector) Type() domain.SourceType { return domain.SourceSmartRecruitersPage }

var jobPathExpr = regexp.MustCompile(`(?i)/jobs/([a-z0-9\-]+)`)

func (c *Connector) Crawl(ctx context.Context, src domain.Source) ([]domain.ExternalJob, []byte, int, error) {
	body, status, err := c.client.Get(ctx, src.BaseURL, nil)
	if err != nil {
		return nil, body, status, err
	}
	matches := jobPathExpr.FindAllSubmatch(body, -1)
	jobs := make([]domain.ExternalJob, 0, len(matches))
	for _, m := range matches {
		id := string(m[1])
		jobs = append(jobs, domain.ExternalJob{ExternalID: id, SourceURL: src.BaseURL, ApplyURL: src.BaseURL + "/jobs/" + id, Title: "SmartRecruiters job", Company: src.BaseURL})
	}
	if len(jobs) == 0 {
		return nil, body, status, fmt.Errorf("no smartrecruiters jobs found")
	}
	return jobs, body, status, nil
}
