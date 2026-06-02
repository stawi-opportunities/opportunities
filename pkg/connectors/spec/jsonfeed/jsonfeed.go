// Package jsonfeed implements the declarative jsonfeed connector type
// for pkg/connectors/spec. It fetches a JSON list URL (optionally
// paginated by an opaque cursor), evaluates JSONPath against the
// response root to find item objects, then evaluates per-field
// JSONPath rooted at each item to populate ExternalOpportunity.
package jsonfeed

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/PaesslerAG/jsonpath"

	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/spec"
	"github.com/stawi-opportunities/opportunities/pkg/content"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

func init() { spec.Register(spec.TypeJSONFeed, &impl{}) }

type impl struct{}

// Crawl returns an iterator that walks a JSON feed via JSONPath
// selectors, with optional cursor-based pagination.
func (impl) Crawl(ctx context.Context, src domain.Source, client *httpx.Client, s *spec.ConnectorSpec) connectors.CrawlIterator {
	cursor := ""
	if s.Pagination != nil {
		cursor = s.Pagination.InitialCursor
	}
	return &iter{
		ctx:    ctx,
		src:    src,
		client: client,
		spec:   s,
		cursor: cursor,
		first:  true,
	}
}

type iter struct {
	ctx    context.Context
	src    domain.Source
	client *httpx.Client
	spec   *spec.ConnectorSpec

	cursor     string
	first      bool
	items      []domain.ExternalOpportunity
	rawBody    []byte
	httpStatus int
	err        error
	done       bool
}

// Next fetches the next page of JSON, evaluates item_selector, and
// stores extracted opportunities for retrieval via Items. Returns
// false when iteration is complete.
func (it *iter) Next(ctx context.Context) bool {
	if it.done {
		return false
	}

	// If pagination is cursor-based and we've drained the feed, stop.
	if !it.first && !it.hasCursor() {
		it.done = true
		return false
	}

	if !it.first && it.spec.DelayMS > 0 {
		select {
		case <-ctx.Done():
			it.err = ctx.Err()
			it.done = true
			return false
		case <-time.After(time.Duration(it.spec.DelayMS) * time.Millisecond):
		}
	}
	it.first = false

	url := strings.ReplaceAll(it.spec.ListURL, "{cursor}", it.cursor)
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
		it.items = nil
		return false
	}

	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		it.err = fmt.Errorf("jsonfeed unmarshal: %w", err)
		it.done = true
		return false
	}

	items, err := extractItems(root, it.spec)
	if err != nil {
		it.err = err
		it.done = true
		return false
	}
	for i := range items {
		items[i].Source = it.src.Type
		items[i].SourceURL = url
	}
	it.items = items

	// Advance cursor if configured.
	nextCursor := ""
	if it.spec.Pagination != nil && it.spec.Pagination.Kind == "cursor" && it.spec.Pagination.CursorPath != "" {
		if v, err := jsonpath.Get(it.spec.Pagination.CursorPath, root); err == nil {
			if s, ok := v.(string); ok {
				nextCursor = s
			}
		}
	}
	it.cursor = nextCursor

	// Stop on empty page if configured.
	if it.spec.Pagination != nil && it.spec.Pagination.StopOnEmpty && len(items) == 0 {
		it.done = true
	}
	return true
}

// hasCursor reports whether the iterator still has pagination work to do.
func (it *iter) hasCursor() bool {
	if it.spec.Pagination == nil {
		return false
	}
	if it.spec.Pagination.Kind != "cursor" {
		return false
	}
	return it.cursor != ""
}

// Items returns the most-recently-fetched batch.
func (it *iter) Items() []domain.ExternalOpportunity { return it.items }

// RawPayload returns the raw JSON body of the last fetch.
func (it *iter) RawPayload() []byte { return it.rawBody }

// HTTPStatus returns the status code of the last HTTP request.
func (it *iter) HTTPStatus() int { return it.httpStatus }

// Err returns the first iteration error, if any.
func (it *iter) Err() error { return it.err }

// Cursor returns the most-recent next-page cursor as an opaque JSON
// token, or nil when no cursor is in play. The token round-trips
// through json.RawMessage so callers can persist + replay it.
func (it *iter) Cursor() json.RawMessage {
	if it.cursor == "" {
		return nil
	}
	b, err := json.Marshal(it.cursor)
	if err != nil {
		return nil
	}
	return b
}

// Content returns nil — JSON feeds carry no whole-page markdown.
func (it *iter) Content() *content.Extracted { return nil }

// extractItems evaluates the spec's item_selector against the parsed
// JSON root and returns one ExternalOpportunity per match.
func extractItems(root any, s *spec.ConnectorSpec) ([]domain.ExternalOpportunity, error) {
	selector := s.Items
	if selector == "" {
		// Default: treat the root as a single item (rare, but supported).
		opp := opportunityFromItem(root, s)
		return []domain.ExternalOpportunity{opp}, nil
	}

	matched, err := jsonpath.Get(selector, root)
	if err != nil {
		// JSONPath errors here typically mean zero matches — treat as empty.
		return nil, nil
	}

	items := toSlice(matched)
	out := make([]domain.ExternalOpportunity, 0, len(items))
	for _, item := range items {
		out = append(out, opportunityFromItem(item, s))
	}
	return out, nil
}

// toSlice normalises a jsonpath result into a flat []any. The library
// returns either a single value, a slice, or an array depending on
// what the selector matched.
func toSlice(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case []map[string]any:
		out := make([]any, 0, len(x))
		for _, m := range x {
			out = append(out, m)
		}
		return out
	case nil:
		return nil
	default:
		return []any{x}
	}
}

// opportunityFromItem evaluates every field selector relative to the
// given item and folds the results into a single ExternalOpportunity.
func opportunityFromItem(item any, s *spec.ConnectorSpec) domain.ExternalOpportunity {
	opp := domain.ExternalOpportunity{}
	for name, fe := range s.Fields {
		value := evaluateField(item, fe.Selector)
		applyField(&opp, name, value, fe.ParseAs)
	}
	return opp
}

// evaluateField runs a JSONPath expression against an item and returns
// the result as a string. Returns "" on miss or non-stringable value.
func evaluateField(item any, selector string) string {
	if selector == "" {
		return ""
	}
	v, err := jsonpath.Get(selector, item)
	if err != nil {
		return ""
	}
	return stringify(v)
}

// stringify coerces a JSONPath result into a string. Numbers, bools,
// and the first element of slices/arrays are accepted; objects come
// back as their JSON encoding so callers can stash them in Attributes.
func stringify(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		// JSON numbers are float64 — print integers cleanly.
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%g", x)
	case bool:
		return fmt.Sprintf("%t", x)
	case []any:
		if len(x) == 0 {
			return ""
		}
		return stringify(x[0])
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
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
	case "apply_url":
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
func parseTime(s, parseAs string) *time.Time {
	if s == "" {
		return nil
	}
	layouts := []string{time.RFC3339, time.RFC3339Nano, "2006-01-02T15:04:05Z", "2006-01-02"}
	switch parseAs {
	case "iso", "":
		// try RFC3339 + common variants.
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}
