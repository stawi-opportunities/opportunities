// Package lever implements a Connector for Lever-hosted job boards via
// Lever's free public Postings API (api.lever.co/v0/postings/{company}).
// One HTTP request returns the company's full posting list as structured
// JSON — the most efficient, reliable way to pull a Lever board (no HTML
// scraping, no JS rendering, no unblocker).
package lever

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	connectors "github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/content"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// Connector crawls Lever-hosted boards.
type Connector struct{ client *httpx.Client }

// New creates a Lever Connector using the provided HTTP client.
func New(client *httpx.Client) *Connector { return &Connector{client: client} }

// apiBaseURL is a var so tests can point the connector at a stub server.
var apiBaseURL = "https://api.lever.co"

// Type returns the SourceType this connector handles.
func (c *Connector) Type() domain.SourceType { return domain.SourceLever }

// company extracts the Lever account slug from a board URL like
// https://jobs.lever.co/meesho (the segment after the host).
func company(baseURL string) string {
	s := strings.TrimSuffix(baseURL, "/")
	if i := strings.LastIndex(s, "lever.co/"); i >= 0 {
		return s[i+len("lever.co/"):]
	}
	return strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
}

// Crawl fetches all postings for the board in one API call. Like
// Greenhouse the endpoint is single-page; the iter records a checkpoint
// so a NATS redelivery can skip a completed page.
func (c *Connector) Crawl(ctx context.Context, src domain.Source) connectors.CrawlIterator {
	return c.crawlFrom(ctx, src, 1, "")
}

// CrawlResume treats a resume past page 1 as "already done".
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

type leverPosting struct {
	ID         string `json:"id"`
	Text       string `json:"text"`
	Country    string `json:"country"`
	HostedURL  string `json:"hostedUrl"`
	ApplyURL   string `json:"applyUrl"`
	DescPlain  string `json:"descriptionPlain"`
	CreatedAt  int64  `json:"createdAt"`
	Workplace  string `json:"workplaceType"`
	Categories struct {
		Location   string `json:"location"`
		Team       string `json:"team"`
		Department string `json:"department"`
		Commitment string `json:"commitment"`
	} `json:"categories"`
}

func (c *Connector) crawlFrom(ctx context.Context, src domain.Source, startPage int, lastURL string) connectors.CrawlIterator {
	u := fmt.Sprintf("%s/v0/postings/%s?mode=json", apiBaseURL, company(src.BaseURL))

	if startPage > 1 {
		return &iter{src: src, page: startPage, consumed: true, lastURL: lastURL}
	}

	body, status, err := c.client.Get(ctx, u, map[string]string{"Accept": "application/json"})
	if err != nil {
		return &iter{src: src, page: startPage, raw: body, httpStatus: status, err: err, lastURL: u}
	}
	if status != 200 {
		return &iter{src: src, page: startPage, raw: body, httpStatus: status,
			err: fmt.Errorf("lever board %q: status %d (board removed?)", company(src.BaseURL), status), lastURL: u}
	}

	var postings []leverPosting
	if jErr := json.Unmarshal(body, &postings); jErr != nil {
		return &iter{src: src, page: startPage, raw: body, httpStatus: status, err: fmt.Errorf("decode lever payload: %w", jErr), lastURL: u}
	}

	out := make([]domain.ExternalOpportunity, 0, len(postings))
	for _, p := range postings {
		apply := p.HostedURL
		if apply == "" {
			apply = p.ApplyURL
		}
		remote := strings.EqualFold(p.Workplace, "remote")
		attrs := map[string]any{}
		if p.Categories.Commitment != "" {
			attrs["employment_type"] = p.Categories.Commitment
		}
		if p.Categories.Team != "" {
			attrs["team"] = p.Categories.Team
		}
		o := domain.ExternalOpportunity{
			Kind:          "job",
			ExternalID:    p.ID,
			SourceURL:     src.BaseURL,
			ApplyURL:      apply,
			Title:         p.Text,
			IssuingEntity: company(src.BaseURL),
			LocationText:  p.Categories.Location,
			Description:   p.DescPlain,
			Remote:        remote,
		}
		if p.CreatedAt > 0 {
			t := time.UnixMilli(p.CreatedAt).UTC()
			o.PostedAt = &t
		}
		if p.Country != "" {
			o.AnchorLocation = &domain.Location{Country: p.Country}
		}
		if len(attrs) > 0 {
			o.Attributes = attrs
		}
		out = append(out, o)
	}

	var ext *content.Extracted
	if len(body) > 0 {
		ext = content.ExtractFromJSON(string(body), "")
	}
	return &iter{src: src, page: startPage, jobs: out, raw: body, httpStatus: status, extracted: ext, lastURL: u}
}

// iter is a single-page CrawlIterator + CheckpointableIterator, mirroring
// the Greenhouse connector.
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

func (it *iter) Checkpoint() *connectors.CheckpointState {
	cursor, _ := json.Marshal(struct {
		Page int `json:"page"`
	}{Page: it.page})
	return &connectors.CheckpointState{Cursor: cursor, PageIdx: it.page, LastURL: it.lastURL}
}
