// Package xmlfeed implements the declarative xmlfeed connector type
// for pkg/connectors/spec. It fetches an XML feed and walks per-item
// matches via XPath: `item_selector` selects each row; each
// `fields` selector is then evaluated relative to that row.
//
// Unlike the rssfeed connector — which assumes RSS 2.0 / Atom shape —
// xmlfeed handles arbitrary publisher XML formats (e.g. Workday
// `<source><job>…`, BambooHR `<jobs>`, etc.) where the schema is
// idiosyncratic enough that no off-the-shelf parser fits.
package xmlfeed

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/antchfx/xmlquery"

	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/spec"
	"github.com/stawi-opportunities/opportunities/pkg/content"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

func init() { spec.Register(spec.TypeXMLFeed, &impl{}) }

type impl struct{}

// Crawl returns a single-shot iterator over the items in an XML feed.
// No pagination — XML feeds in the wild are typically single-payload.
func (impl) Crawl(ctx context.Context, src domain.Source, client *httpx.Client, s *spec.ConnectorSpec) connectors.CrawlIterator {
	body, status, err := client.Get(ctx, s.ListURL, s.Headers)
	if err != nil {
		return connectors.NewSinglePageIterator(nil, body, status, err)
	}
	if status != 200 {
		return connectors.NewSinglePageIterator(nil, body, status, nil)
	}

	doc, parseErr := xmlquery.Parse(strings.NewReader(string(body)))
	if parseErr != nil {
		return connectors.NewSinglePageIterator(nil, body, status, parseErr)
	}

	itemXPath := s.Items
	if itemXPath == "" {
		// No item selector → treat the document root as a single item.
		// Rare, but cheap to support.
		opp := opportunityFromNode(doc, s)
		opp.Source = src.Type
		opp.SourceURL = s.ListURL
		return &iter{items: []domain.ExternalOpportunity{opp}, rawBody: body, httpStatus: status}
	}

	nodes, qErr := xmlquery.QueryAll(doc, itemXPath)
	if qErr != nil {
		return connectors.NewSinglePageIterator(nil, body, status, qErr)
	}
	items := make([]domain.ExternalOpportunity, 0, len(nodes))
	for _, n := range nodes {
		opp := opportunityFromNode(n, s)
		opp.Source = src.Type
		opp.SourceURL = s.ListURL
		items = append(items, opp)
	}

	return &iter{items: items, rawBody: body, httpStatus: status}
}

type iter struct {
	items      []domain.ExternalOpportunity
	rawBody    []byte
	httpStatus int
	consumed   bool
}

// Next yields the entire XML batch on the first call, then returns
// false on every subsequent call.
func (it *iter) Next(_ context.Context) bool {
	if it.consumed {
		return false
	}
	it.consumed = true
	return true
}

// Items returns every parsed feed item.
func (it *iter) Items() []domain.ExternalOpportunity { return it.items }

// RawPayload returns the raw XML body.
func (it *iter) RawPayload() []byte { return it.rawBody }

// HTTPStatus returns the status code of the feed fetch.
func (it *iter) HTTPStatus() int { return it.httpStatus }

// Err always returns nil — fetch + parse errors are surfaced via a
// SinglePageIterator constructed at Crawl time.
func (it *iter) Err() error { return nil }

// Cursor returns nil — XML feeds carry no pagination state.
func (it *iter) Cursor() json.RawMessage { return nil }

// Content returns nil — per-item descriptions are mapped directly.
func (it *iter) Content() *content.Extracted { return nil }

// opportunityFromNode evaluates every field XPath against an item
// node and folds the results into an ExternalOpportunity.
func opportunityFromNode(node *xmlquery.Node, s *spec.ConnectorSpec) domain.ExternalOpportunity {
	opp := domain.ExternalOpportunity{}
	for name, fe := range s.Fields {
		value := evaluateField(node, fe.Selector)
		applyField(&opp, name, value, fe.ParseAs)
	}
	return opp
}

// evaluateField runs an XPath expression against an item node and
// returns the result's text content. Returns "" on miss.
func evaluateField(node *xmlquery.Node, selector string) string {
	if selector == "" {
		return ""
	}
	hit := xmlquery.FindOne(node, selector)
	if hit == nil {
		return ""
	}
	return strings.TrimSpace(hit.InnerText())
}

// applyField maps a YAML field name to the right ExternalOpportunity
// struct field. Unknown names land in Attributes.
func applyField(opp *domain.ExternalOpportunity, name, value, parseAs string) {
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
	case "apply_url", "url":
		opp.ApplyURL = value
	case "location_text", "location":
		opp.LocationText = value
	case "external_id":
		opp.ExternalID = value
	case "currency":
		opp.Currency = value
	case "posted_at":
		opp.PostedAt = parseTime(value, parseAs)
	case "deadline":
		opp.Deadline = parseTime(value, parseAs)
	default:
		if opp.Attributes == nil {
			opp.Attributes = map[string]any{}
		}
		opp.Attributes[name] = value
	}
}

// parseTime tries the common timestamp shapes. RFC3339 is the default;
// other layouts can be requested via parse_as in the spec.
func parseTime(s, _ string) *time.Time {
	if s == "" {
		return nil
	}
	layouts := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05Z",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}
