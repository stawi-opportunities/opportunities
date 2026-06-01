// Package rssfeed implements the declarative rssfeed connector type
// for pkg/connectors/spec. It fetches an RSS 2.0 or Atom feed via
// gofeed (which transparently handles both formats), then maps each
// gofeed.Item to ExternalOpportunity using a spec-supplied field
// name → connector field mapping.
//
// Pagination is not supported — RSS/Atom feeds are inherently
// single-shot — and the Pagination block in the spec is ignored.
package rssfeed

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"

	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/spec"
	"github.com/stawi-opportunities/opportunities/pkg/content"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

func init() { spec.Register(spec.TypeRSSFeed, &impl{}) }

type impl struct{}

// Crawl returns a single-shot iterator over the items in an RSS/Atom
// feed. The whole feed is fetched + parsed during Crawl; the
// iterator's Next yields true exactly once on the first call.
func (impl) Crawl(ctx context.Context, src domain.Source, client *httpx.Client, s *spec.ConnectorSpec) connectors.CrawlIterator {
	body, status, err := client.Get(ctx, s.ListURL, s.Headers)
	if err != nil {
		return connectors.NewSinglePageIterator(nil, body, status, err)
	}
	if status != 200 {
		return connectors.NewSinglePageIterator(nil, body, status, nil)
	}

	feed, parseErr := gofeed.NewParser().ParseString(string(body))
	if parseErr != nil {
		return connectors.NewSinglePageIterator(nil, body, status, parseErr)
	}

	items := make([]domain.ExternalOpportunity, 0, len(feed.Items))
	for _, item := range feed.Items {
		opp := domain.ExternalOpportunity{
			Source:    src.Type,
			SourceURL: s.ListURL,
		}
		for name, fe := range s.Fields {
			applyFieldFromItem(&opp, name, fe.Selector, item)
		}
		items = append(items, opp)
	}

	return &iter{
		items:      items,
		rawBody:    body,
		httpStatus: status,
	}
}

// iter is a single-page iterator. We don't reuse SinglePageIterator
// here because we want explicit control over Content() (always nil
// for RSS) without the JSON/HTML extraction heuristic.
type iter struct {
	items      []domain.ExternalOpportunity
	rawBody    []byte
	httpStatus int
	consumed   bool
}

// Next yields the entire RSS feed batch on the first call, then
// returns false thereafter.
func (it *iter) Next(_ context.Context) bool {
	if it.consumed {
		return false
	}
	it.consumed = true
	return true
}

// Items returns every parsed feed item.
func (it *iter) Items() []domain.ExternalOpportunity { return it.items }

// RawPayload returns the raw RSS/Atom XML body.
func (it *iter) RawPayload() []byte { return it.rawBody }

// HTTPStatus returns the status code of the feed fetch.
func (it *iter) HTTPStatus() int { return it.httpStatus }

// Err always returns nil — fetch + parse errors are surfaced via a
// SinglePageIterator constructed at Crawl time.
func (it *iter) Err() error { return nil }

// Cursor returns nil — RSS feeds have no pagination.
func (it *iter) Cursor() json.RawMessage { return nil }

// Content returns nil — per-item descriptions are mapped directly.
func (it *iter) Content() *content.Extracted { return nil }

// applyFieldFromItem reads a gofeed.Item field (selected by name,
// case-insensitive) and writes the result onto the destination
// ExternalOpportunity. Unknown opp fields land in Attributes.
func applyFieldFromItem(opp *domain.ExternalOpportunity, oppFieldName, itemFieldSelector string, item *gofeed.Item) {
	value, t := lookupItemField(item, itemFieldSelector)
	if value == "" && t == nil {
		return
	}
	switch oppFieldName {
	case "title":
		opp.Title = value
	case "description":
		opp.Description = value
	case "issuing_entity", "company", "author":
		opp.IssuingEntity = value
	case "apply_url", "link":
		opp.ApplyURL = value
	case "location_text", "location":
		opp.LocationText = value
	case "external_id":
		opp.ExternalID = value
	case "posted_at":
		if t != nil {
			opp.PostedAt = t
		} else if parsed := tryParseTime(value); parsed != nil {
			opp.PostedAt = parsed
		}
	default:
		if value == "" {
			return
		}
		if opp.Attributes == nil {
			opp.Attributes = map[string]any{}
		}
		opp.Attributes[oppFieldName] = value
	}
}

// lookupItemField reads a single named field from a gofeed.Item. The
// matcher is case-insensitive and accepts common RSS/Atom synonyms.
// Returns (stringValue, parsedTime) — parsedTime is non-nil only when
// the field naturally carries a parsed timestamp (pubDate, updated).
func lookupItemField(item *gofeed.Item, name string) (string, *time.Time) {
	switch strings.ToLower(name) {
	case "title":
		return item.Title, nil
	case "description":
		return item.Description, nil
	case "content":
		return item.Content, nil
	case "link":
		return item.Link, nil
	case "guid":
		return item.GUID, nil
	case "author":
		if item.Author != nil && item.Author.Name != "" {
			return item.Author.Name, nil
		}
		if len(item.Authors) > 0 && item.Authors[0] != nil {
			return item.Authors[0].Name, nil
		}
		return "", nil
	case "pubdate", "published":
		return item.Published, item.PublishedParsed
	case "updated":
		return item.Updated, item.UpdatedParsed
	case "categories":
		return strings.Join(item.Categories, ","), nil
	default:
		// Try Custom map (RSS namespace extensions parsed loosely).
		if v, ok := item.Custom[name]; ok {
			return v, nil
		}
		return "", nil
	}
}

// tryParseTime is a fallback parser for RSS feeds whose pubDate field
// gofeed didn't auto-parse. Tries RFC1123Z first (the RSS 2.0 default).
func tryParseTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	layouts := []string{
		time.RFC1123Z,
		time.RFC1123,
		time.RFC822Z,
		time.RFC822,
		time.RFC3339,
		time.RFC3339Nano,
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}
