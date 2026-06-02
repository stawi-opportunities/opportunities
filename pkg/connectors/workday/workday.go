// Package workday implements a Connector for Workday job boards.
package workday

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	connectors "github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/content"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// Connector crawls Workday-hosted job boards.
type Connector struct{ client *httpx.Client }

// New creates a Workday Connector using the provided HTTP client.
func New(client *httpx.Client) *Connector { return &Connector{client: client} }

// Type returns the SourceType this connector handles.
func (c *Connector) Type() domain.SourceType { return domain.SourceWorkday }

// Crawl fetches all job postings from the Workday API for the given source.
// Workday's /wday/cxs/jobs endpoint returns the full first batch in one
// response; like Greenhouse we model it as page=1 and emit a checkpoint
// so the resume path can short-circuit a redelivery that already
// processed the page.
func (c *Connector) Crawl(ctx context.Context, src domain.Source) connectors.CrawlIterator {
	return c.crawlFrom(ctx, src, 1, "")
}

// CrawlResume picks up at the page index recorded in cp. For Workday
// (single-page CXS endpoint) a non-zero resume page just means
// "you already processed this; treat it as done" — the iter reports
// consumed=true on construction. Behaves identically to Crawl when
// cp is nil or its page <= 1.
func (c *Connector) CrawlResume(ctx context.Context, src domain.Source, cp *connectors.CheckpointState) connectors.CrawlIterator {
	startPage := 1
	lastURL := ""
	if cp != nil {
		var s struct {
			Page int `json:"page"`
		}
		if err := json.Unmarshal(cp.Cursor, &s); err == nil && s.Page > 0 {
			startPage = s.Page
		}
		lastURL = cp.LastURL
	}
	return c.crawlFrom(ctx, src, startPage, lastURL)
}

func (c *Connector) crawlFrom(ctx context.Context, src domain.Source, startPage int, lastURL string) connectors.CrawlIterator {
	base := strings.TrimSuffix(src.BaseURL, "/")
	u := base + "/wday/cxs/jobs"

	if startPage > 1 {
		return &iter{src: src, page: startPage, consumed: true, lastURL: lastURL}
	}

	body, status, err := c.client.Get(ctx, u, nil)
	if err != nil {
		return &iter{src: src, page: startPage, raw: body, httpStatus: status, err: err, lastURL: u}
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
	if jErr := json.Unmarshal(body, &payload); jErr != nil {
		return &iter{src: src, page: startPage, raw: body, httpStatus: status, err: fmt.Errorf("decode workday payload: %w", jErr), lastURL: u}
	}

	out := make([]domain.ExternalOpportunity, 0, len(payload.JobPostings))
	for _, j := range payload.JobPostings {
		d := strings.Join(j.BulletFields, " ")
		out = append(out, domain.ExternalOpportunity{
			Kind:          "job",
			ExternalID:    j.ID,
			SourceURL:     src.BaseURL,
			ApplyURL:      base + "/job/" + j.ExternalPath,
			Title:         j.Title,
			IssuingEntity: base,
			LocationText:  j.LocationsText,
			Description:   d,
		})
	}

	var ext *content.Extracted
	if len(body) > 0 {
		ext = content.ExtractFromJSON(string(body), "")
	}
	return &iter{
		src:        src,
		page:       startPage,
		jobs:       out,
		raw:        body,
		httpStatus: status,
		extracted:  ext,
		lastURL:    u,
	}
}

// iter is a single-page CrawlIterator that also implements
// CheckpointableIterator. See greenhouse iter for the per-page
// semantics — Workday tracks page state the same way.
type iter struct {
	src        domain.Source
	page       int
	jobs       []domain.ExternalOpportunity
	raw        []byte
	httpStatus int
	err        error
	consumed   bool
	extracted  *content.Extracted
	lastURL    string
}

func (it *iter) Next(_ context.Context) bool {
	if it.err != nil || it.consumed {
		return false
	}
	it.consumed = true
	it.page++
	return true
}

func (it *iter) Items() []domain.ExternalOpportunity { return it.jobs }
func (it *iter) RawPayload() []byte                  { return it.raw }
func (it *iter) HTTPStatus() int                     { return it.httpStatus }
func (it *iter) Err() error                          { return it.err }
func (it *iter) Cursor() json.RawMessage             { return nil }
func (it *iter) Content() *content.Extracted         { return it.extracted }

// Checkpoint serializes the iter's page index + last-fetched URL for
// the crawl handler to persist. Implements connectors.CheckpointableIterator.
func (it *iter) Checkpoint() *connectors.CheckpointState {
	cursor, _ := json.Marshal(struct {
		Page int `json:"page"`
	}{Page: it.page})
	return &connectors.CheckpointState{
		Cursor:  cursor,
		PageIdx: it.page,
		LastURL: it.lastURL,
	}
}
