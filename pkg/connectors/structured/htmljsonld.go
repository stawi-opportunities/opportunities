// Package structured provides non-AI connectors that extract jobs from
// structured page data (schema.org JobPosting JSON-LD). Used for source
// types that previously relied on the universal AI stub path.
package structured

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/spec/schemaorgjsonld"
	"github.com/stawi-opportunities/opportunities/pkg/content"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// HTMLJSONLD fetches a source BaseURL (or configured list page) and extracts
// every schema.org JobPosting from JSON-LD blocks. No LLM, no URL stubs.
type HTMLJSONLD struct {
	client   *httpx.Client
	srcType  domain.SourceType
}

// NewHTMLJSONLD builds a connector that reports the given SourceType.
func NewHTMLJSONLD(client *httpx.Client, st domain.SourceType) *HTMLJSONLD {
	return &HTMLJSONLD{client: client, srcType: st}
}

// Type implements connectors.Connector.
func (c *HTMLJSONLD) Type() domain.SourceType { return c.srcType }

// Crawl fetches the source base URL once and maps all JobPostings.
func (c *HTMLJSONLD) Crawl(ctx context.Context, source domain.Source) connectors.CrawlIterator {
	url := strings.TrimSpace(source.BaseURL)
	if url == "" {
		return connectors.NewSinglePageIterator(nil, nil, 0, nil)
	}
	body, status, err := c.client.Get(ctx, url, map[string]string{
		"Accept": "text/html,application/xhtml+xml",
	})
	if err != nil {
		return connectors.NewSinglePageIterator(nil, body, status, err)
	}
	if status != 200 {
		return connectors.NewSinglePageIterator(nil, body, status, nil)
	}
	postings := schemaorgjsonld.ExtractJobPostings(body)
	items := make([]domain.ExternalOpportunity, 0, len(postings))
	for _, raw := range postings {
		opp, mapErr := schemaorgjsonld.MapJobPosting(raw)
		if mapErr != nil || opp == nil {
			continue
		}
		if strings.TrimSpace(opp.Title) == "" || strings.TrimSpace(opp.IssuingEntity) == "" {
			continue
		}
		opp.Source = c.srcType
		opp.SourceURL = url
		if strings.TrimSpace(opp.ApplyURL) == "" {
			opp.ApplyURL = url
		}
		items = append(items, *opp)
	}
	return &pageIter{items: items, raw: body, status: status}
}

type pageIter struct {
	items  []domain.ExternalOpportunity
	raw    []byte
	status int
	done   bool
}

func (it *pageIter) Next(context.Context) bool {
	if it.done {
		return false
	}
	it.done = true
	return len(it.items) > 0
}
func (it *pageIter) Items() []domain.ExternalOpportunity { return it.items }
func (it *pageIter) RawPayload() []byte                   { return it.raw }
func (it *pageIter) HTTPStatus() int                      { return it.status }
func (it *pageIter) Err() error                           { return nil }
func (it *pageIter) Cursor() json.RawMessage              { return nil }
func (it *pageIter) Content() *content.Extracted          { return nil }
