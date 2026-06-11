// Package greenhouse implements a Connector for Greenhouse job boards.
package greenhouse

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

// Connector crawls Greenhouse-hosted job boards.
type Connector struct{ client *httpx.Client }

// New creates a Greenhouse Connector using the provided HTTP client.
func New(client *httpx.Client) *Connector { return &Connector{client: client} }

// apiBaseURL is a var so tests can point the connector at a stub server.
var apiBaseURL = "https://boards-api.greenhouse.io"

// Type returns the SourceType this connector handles.
func (c *Connector) Type() domain.SourceType { return domain.SourceGreenhouse }

// Crawl fetches all jobs from the Greenhouse boards API for the given source.
// Greenhouse returns the full job list in one HTTP response (no pagination
// on the boards-api endpoint), but we still emit a page-tracking iter
// so the crawl handler can record a checkpoint and the next redelivery
// can call CrawlResume — useful when a NATS redelivery wants to skip
// re-fetching a successful page (page=2 means "already done page 1").
func (c *Connector) Crawl(ctx context.Context, src domain.Source) connectors.CrawlIterator {
	return c.crawlFrom(ctx, src, 1, "")
}

// CrawlResume picks up at the page index recorded in cp. For Greenhouse
// the API is single-page so a non-zero resume page just means "you
// already processed this; treat it as done" — the iter will report
// consumed=true on construction so Next() returns false immediately.
// When cp is nil or its page <= 1, behaves identically to Crawl.
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
	company := strings.Trim(strings.TrimPrefix(src.BaseURL, "https://boards.greenhouse.io/"), "/")
	u := fmt.Sprintf("%s/v1/boards/%s/jobs?content=true", apiBaseURL, company)

	// Resume past page 1 on a single-page API: nothing to fetch. Emit
	// an iterator that immediately reports consumed=true so the
	// handler exits the loop cleanly, then clears the checkpoint.
	if startPage > 1 {
		return &iter{src: src, page: startPage, consumed: true, lastURL: lastURL}
	}

	body, status, err := c.client.Get(ctx, u, nil)
	if err != nil {
		return &iter{src: src, page: startPage, raw: body, httpStatus: status, err: err, lastURL: u}
	}
	// A non-200 here is a dead/renamed board token, not an empty board:
	// the API 404s with an error body that unmarshals into zero jobs, so
	// without this guard a company leaving Greenhouse shows up as weeks
	// of "successful" zero-yield crawls (19 of 90 prod sources, found in
	// the 2026-06-11 source audit). Fail loudly so health decay + the
	// degraded transition make the dead board visible.
	if status != 200 {
		return &iter{src: src, page: startPage, raw: body, httpStatus: status,
			err: fmt.Errorf("greenhouse board %q: status %d (board removed or renamed?)", company, status), lastURL: u}
	}

	var payload struct {
		Jobs []struct {
			ID       int64  `json:"id"`
			Title    string `json:"title"`
			Absolute string `json:"absolute_url"`
			Location struct {
				Name string `json:"name"`
			} `json:"location"`
			Content string `json:"content"`
		} `json:"jobs"`
	}
	if jErr := json.Unmarshal(body, &payload); jErr != nil {
		return &iter{src: src, page: startPage, raw: body, httpStatus: status, err: fmt.Errorf("decode greenhouse payload: %w", jErr), lastURL: u}
	}

	out := make([]domain.ExternalOpportunity, 0, len(payload.Jobs))
	for _, j := range payload.Jobs {
		out = append(out, domain.ExternalOpportunity{
			Kind:          "job",
			ExternalID:    fmt.Sprintf("%d", j.ID),
			SourceURL:     src.BaseURL,
			ApplyURL:      j.Absolute,
			Title:         j.Title,
			IssuingEntity: company,
			LocationText:  j.Location.Name,
			Description:   j.Content,
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
// CheckpointableIterator. Greenhouse returns the full job list in one
// response so page advances from N to N+1 on the single Next() call —
// the checkpoint records the next page index, which is what the
// resume path reads.
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

// Next returns true the first time it is called (yielding the wrapped
// batch), false on subsequent calls. Iterators constructed with err
// set or consumed=true (resume past page 1) report false immediately.
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
