// Package htmllisting implements the declarative htmllisting connector
// type for pkg/connectors/spec. It fetches a list URL (optionally
// paginated), parses the HTML with goquery, applies the spec's
// item_selector to find listing rows, and extracts per-row fields via
// CSS selectors with the "::text", "::html", or "::attr(name)" suffix
// syntax.
package htmllisting

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/spec"
	"github.com/stawi-opportunities/opportunities/pkg/content"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

func init() { spec.Register(spec.TypeHTMLListing, &impl{}) }

type impl struct{}

// Crawl returns an iterator that walks pages of an HTML listing.
func (impl) Crawl(ctx context.Context, src domain.Source, client *httpx.Client, s *spec.ConnectorSpec) connectors.CrawlIterator {
	return &iter{
		ctx:    ctx,
		src:    src,
		client: client,
		spec:   s,
		page:   paginationStart(s.Pagination),
	}
}

// paginationStart returns the page number to begin iteration at.
// Defaults to 1 when no pagination is configured.
func paginationStart(p *spec.Pagination) int {
	if p == nil || p.Start == 0 {
		return 1
	}
	return p.Start
}

// paginationStep returns the increment between pages. Defaults to 1.
func paginationStep(p *spec.Pagination) int {
	if p == nil || p.Step == 0 {
		return 1
	}
	return p.Step
}

type iter struct {
	ctx    context.Context
	src    domain.Source
	client *httpx.Client
	spec   *spec.ConnectorSpec

	page       int
	items      []domain.ExternalOpportunity
	rawBody    []byte
	httpStatus int
	err        error
	done       bool
}

// Next fetches the next page, applies the item + field selectors, and
// stores the resulting opportunities for retrieval via Items. Returns
// false when iteration is complete (max page reached, empty page with
// stop_on_empty, error, or HTTP failure).
func (it *iter) Next(ctx context.Context) bool {
	if it.done {
		return false
	}

	if it.spec.Pagination != nil && it.spec.Pagination.Max > 0 && it.page > it.spec.Pagination.Max {
		it.done = true
		return false
	}

	if it.spec.DelayMS > 0 && it.page > paginationStart(it.spec.Pagination) {
		select {
		case <-ctx.Done():
			it.err = ctx.Err()
			it.done = true
			return false
		case <-time.After(time.Duration(it.spec.DelayMS) * time.Millisecond):
		}
	}

	url := strings.ReplaceAll(it.spec.ListURL, "{page}", strconv.Itoa(it.page))
	body, status, err := it.client.Get(ctx, url, it.spec.Headers)
	it.rawBody = body
	it.httpStatus = status
	if err != nil {
		it.err = err
		it.done = true
		return false
	}
	if status != 200 {
		it.done = true
		// Surface a non-200 final page as no items but no error — the
		// caller treats an empty Items() like any quiet end of feed.
		it.items = nil
		return false
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		it.err = err
		it.done = true
		return false
	}

	items := make([]domain.ExternalOpportunity, 0)
	selection := doc.Find(it.spec.Items)
	selection.Each(func(_ int, sel *goquery.Selection) {
		opp := domain.ExternalOpportunity{
			Source:    it.src.Type,
			SourceURL: url,
		}
		for name, fe := range it.spec.Fields {
			v := extractField(sel, fe)
			applyField(&opp, name, v)
		}
		items = append(items, opp)
	})
	it.items = items

	// Stop on the first empty page if so configured.
	if it.spec.Pagination != nil && it.spec.Pagination.StopOnEmpty && len(items) == 0 {
		it.done = true
	}

	it.page += paginationStep(it.spec.Pagination)
	return true
}

// Items returns the most-recently-fetched batch of opportunities.
func (it *iter) Items() []domain.ExternalOpportunity { return it.items }

// RawPayload returns the raw HTML body of the last page fetched.
func (it *iter) RawPayload() []byte { return it.rawBody }

// HTTPStatus returns the status code of the last HTTP request.
func (it *iter) HTTPStatus() int { return it.httpStatus }

// Err returns the first iteration error, if any.
func (it *iter) Err() error { return it.err }

// Cursor returns nil — HTML listing pagination is purely page-counter
// driven, so there is no opaque resume token to expose.
func (it *iter) Cursor() json.RawMessage { return nil }

// Content returns nil — the iterator does not perform whole-page
// content extraction; per-item Description fields are populated
// directly from the spec's field selectors.
func (it *iter) Content() *content.Extracted { return nil }

// extractField parses the selector syntax "css::text", "css::attr(name)",
// "css::html", or bare "css" (defaults to text) and returns the first
// matching value. parse_as transformations are applied via
// applyParseAs.
func extractField(sel *goquery.Selection, fe spec.FieldExtractor) string {
	css := fe.Selector
	extractor := "text"
	var attr string
	if idx := strings.Index(css, "::"); idx >= 0 {
		css, extractor = css[:idx], css[idx+2:]
		if strings.HasPrefix(extractor, "attr(") && strings.HasSuffix(extractor, ")") {
			attr = extractor[5 : len(extractor)-1]
			extractor = "attr"
		}
	}

	found := sel
	if css != "" {
		first := sel.Find(css).First()
		if first.Length() > 0 {
			found = first
		}
	}

	var raw string
	switch extractor {
	case "text", "":
		raw = strings.TrimSpace(found.Text())
	case "html":
		raw, _ = found.Html()
		raw = strings.TrimSpace(raw)
	case "attr":
		raw, _ = found.Attr(attr)
		raw = strings.TrimSpace(raw)
	}
	return applyParseAs(raw, fe.ParseAs)
}

// applyParseAs applies the parse_as transformation declared in the
// spec. Date variants (iso, relative_time, epoch, unix_ms) are passed
// through as-is — the downstream normalize stage handles the actual
// timestamp parsing. html_to_markdown returns the raw HTML for now;
// a real converter is a follow-up.
func applyParseAs(raw, kind string) string {
	switch kind {
	case "", "iso", "relative_time", "epoch", "unix_ms":
		return raw
	case "html_to_markdown":
		// TODO: hook up a real HTML-to-Markdown converter (e.g. the
		// JohannesKaufmann/html-to-markdown dep already vendored).
		return raw
	}
	return raw
}

// applyField maps a YAML field name to the right ExternalOpportunity
// struct field. Unknown names land in Attributes so spec authors can
// stash custom data without losing it.
func applyField(opp *domain.ExternalOpportunity, name, value string) {
	if value == "" {
		return
	}
	switch name {
	case "title":
		opp.Title = value
	case "description":
		opp.Description = value
	case "issuing_entity", "company":
		opp.IssuingEntity = value
	case "apply_url":
		opp.ApplyURL = value
	case "location_text", "location":
		opp.LocationText = value
	case "external_id":
		opp.ExternalID = value
	case "posted_at":
		if t, err := time.Parse(time.RFC3339, value); err == nil {
			opp.PostedAt = &t
		}
	default:
		if opp.Attributes == nil {
			opp.Attributes = map[string]any{}
		}
		opp.Attributes[name] = value
	}
}
