// Package schemaorgjsonld implements the declarative schemaorgjsonld
// connector type for pkg/connectors/spec. It fetches a single HTML
// page, walks every <script type="application/ld+json"> block, and
// extracts schema.org JobPosting entries into ExternalOpportunity.
//
// The MapJobPosting helper is exported so other connectors (notably
// sitemap, which uses this impl as its detail-fetch fallback) can map
// JSON-LD blocks they have already retrieved without depending on the
// full iterator.
package schemaorgjsonld

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/spec"
	"github.com/stawi-opportunities/opportunities/pkg/content"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

func init() { spec.Register(spec.TypeSchemaOrgJSONLD, &impl{}) }

type impl struct{}

// Crawl returns a single-shot iterator that fetches list_url, parses
// every JSON-LD JobPosting it finds, and maps each into an
// ExternalOpportunity. No pagination — a single HTTP request feeds the
// entire batch.
func (impl) Crawl(ctx context.Context, src domain.Source, client *httpx.Client, s *spec.ConnectorSpec) connectors.CrawlIterator {
	body, status, err := client.Get(ctx, s.ListURL, s.Headers)
	if err != nil {
		return connectors.NewSinglePageIterator(nil, body, status, err)
	}
	if status != 200 {
		return connectors.NewSinglePageIterator(nil, body, status, nil)
	}

	postings := ExtractJobPostings(body)
	items := make([]domain.ExternalOpportunity, 0, len(postings))
	for _, raw := range postings {
		opp, mapErr := MapJobPosting(raw)
		if mapErr != nil || opp == nil {
			continue
		}
		opp.Source = src.Type
		opp.SourceURL = s.ListURL
		items = append(items, *opp)
	}

	return &iter{items: items, rawBody: body, httpStatus: status}
}

// iter is a single-page iterator. We don't reuse SinglePageIterator
// because Content() must stay nil — the JSON-LD-extracted batch is the
// canonical payload, not whole-page text.
type iter struct {
	items      []domain.ExternalOpportunity
	rawBody    []byte
	httpStatus int
	consumed   bool
}

// Next yields the whole-page batch on the first call, then returns
// false on every subsequent call.
func (it *iter) Next(_ context.Context) bool {
	if it.consumed {
		return false
	}
	it.consumed = true
	return true
}

// Items returns the JobPostings extracted from the page.
func (it *iter) Items() []domain.ExternalOpportunity { return it.items }

// RawPayload returns the raw HTML body of the page fetched.
func (it *iter) RawPayload() []byte { return it.rawBody }

// HTTPStatus returns the status code of the page fetch.
func (it *iter) HTTPStatus() int { return it.httpStatus }

// Err returns nil — fetch errors are surfaced via a SinglePageIterator
// constructed at Crawl time.
func (it *iter) Err() error { return nil }

// Cursor returns nil — schemaorgjsonld is single-shot.
func (it *iter) Cursor() json.RawMessage { return nil }

// Content returns nil — per-item Description fields are populated
// directly from the JSON-LD JobPosting blocks.
func (it *iter) Content() *content.Extracted { return nil }

// ExtractJobPostings walks every <script type="application/ld+json">
// block in an HTML body, JSON-parses each, and recursively collects
// JobPosting entries. JobPostings buried in @graph arrays are
// discovered too. When publishers emit a JSON-LD @graph with
// cross-node @id references (BrighterMonday, Jobberman, etc.), those
// refs are resolved before the posting is returned so MapJobPosting
// sees inline hiringOrganization.name / jobLocation.address rather
// than pure {"@id":"..."} stubs. Exported so the sitemap connector
// can reuse it as its default detail-fetch fallback.
func ExtractJobPostings(html []byte) []json.RawMessage {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(html)))
	if err != nil {
		return nil
	}
	var out []json.RawMessage
	doc.Find(`script[type="application/ld+json"]`).Each(func(_ int, sel *goquery.Selection) {
		raw := strings.TrimSpace(sel.Text())
		if raw == "" {
			return
		}
		var parsed any
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			return
		}
		index := map[string]map[string]any{}
		collectGraphIndex(parsed, index)
		out = append(out, walkForJobPostings(parsed, index)...)
	})
	return out
}

// collectGraphIndex indexes every object that carries an @id so later
// pure-reference nodes ({"@id": "..."}) can be expanded against it.
func collectGraphIndex(v any, index map[string]map[string]any) {
	switch x := v.(type) {
	case map[string]any:
		if id, ok := x["@id"].(string); ok && id != "" {
			// First writer wins so a fully-populated node is not
			// overwritten by a later sparse reference to the same id.
			if _, exists := index[id]; !exists {
				index[id] = x
			} else if len(x) > len(index[id]) {
				index[id] = x
			}
		}
		for _, child := range x {
			collectGraphIndex(child, index)
		}
	case []any:
		for _, child := range x {
			collectGraphIndex(child, index)
		}
	}
}

// walkForJobPostings descends arbitrary JSON-LD structures looking for
// objects with @type=="JobPosting" (or arrays of types containing it).
// Each match is reference-resolved against index and re-encoded as a
// json.RawMessage for downstream MapJobPosting consumption.
func walkForJobPostings(v any, index map[string]map[string]any) []json.RawMessage {
	var out []json.RawMessage
	switch x := v.(type) {
	case map[string]any:
		if isJobPostingType(x["@type"]) {
			resolved := resolveJSONLD(x, index, map[string]bool{})
			if m, ok := resolved.(map[string]any); ok {
				if b, err := json.Marshal(m); err == nil {
					out = append(out, b)
				}
			}
		}
		// Recurse into nested values, e.g. @graph arrays.
		for _, child := range x {
			out = append(out, walkForJobPostings(child, index)...)
		}
	case []any:
		for _, child := range x {
			out = append(out, walkForJobPostings(child, index)...)
		}
	}
	return out
}

// resolveJSONLD expands {"@id": "..."} references against index and
// merges referenced node fields under the local object. Cycle-safe via
// seen. Used so JobPosting.hiringOrganization that only carries an @id
// gains Organization.name from the sibling @graph node.
func resolveJSONLD(v any, index map[string]map[string]any, seen map[string]bool) any {
	switch x := v.(type) {
	case map[string]any:
		id, hasID := x["@id"].(string)
		if hasID && id != "" {
			if seen[id] {
				// Cycle: return a shallow copy without further expansion.
				cp := make(map[string]any, len(x))
				for k, vv := range x {
					cp[k] = vv
				}
				return cp
			}
			seen[id] = true
			defer delete(seen, id)
		}

		// Start from the indexed node when present so pure refs expand,
		// then overlay local keys (local wins — e.g. jobLocation may
		// carry address inline while @id points at a Place node).
		base := map[string]any{}
		if hasID && id != "" {
			if ref, ok := index[id]; ok {
				for k, vv := range ref {
					base[k] = vv
				}
			}
		}
		for k, vv := range x {
			base[k] = vv
		}
		out := make(map[string]any, len(base))
		for k, vv := range base {
			out[k] = resolveJSONLD(vv, index, seen)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, child := range x {
			out[i] = resolveJSONLD(child, index, seen)
		}
		return out
	default:
		return v
	}
}

// isJobPostingType reports whether a JSON-LD @type value names
// JobPosting. Accepts either a bare string or a slice of strings.
func isJobPostingType(t any) bool {
	switch x := t.(type) {
	case string:
		return x == "JobPosting"
	case []any:
		for _, v := range x {
			if s, ok := v.(string); ok && s == "JobPosting" {
				return true
			}
		}
	}
	return false
}

// MapJobPosting maps a single JSON-LD JobPosting block (the raw object,
// not the wrapping script tag) onto an ExternalOpportunity. Unknown
// fields are ignored; standard schema.org fields are translated per the
// table in plan B2 Task 6.
func MapJobPosting(raw json.RawMessage) (*domain.ExternalOpportunity, error) {
	var jp map[string]any
	if err := json.Unmarshal(raw, &jp); err != nil {
		return nil, fmt.Errorf("MapJobPosting: %w", err)
	}
	opp := &domain.ExternalOpportunity{Kind: "job"}

	if v, ok := jp["title"].(string); ok {
		opp.Title = v
	}
	if v, ok := jp["description"].(string); ok {
		opp.Description = v
	}
	opp.IssuingEntity = readHiringOrganization(jp["hiringOrganization"])
	if logo := readOrganizationLogo(jp["hiringOrganization"]); logo != "" {
		if opp.Attributes == nil {
			opp.Attributes = map[string]any{}
		}
		opp.Attributes["company_logo"] = logo
	}
	if t := parseDate(stringValue(jp["datePosted"])); t != nil {
		opp.PostedAt = t
	}
	if t := parseDate(stringValue(jp["validThrough"])); t != nil {
		opp.Deadline = t
	}
	if v := stringValue(jp["employmentType"]); v != "" {
		ensureAttrs(opp)["employment_type"] = v
	}
	loc := readJobLocation(jp["jobLocation"])
	if loc != nil && !loc.IsZero() {
		opp.AnchorLocation = loc
		if opp.LocationText == "" {
			opp.LocationText = locationToText(*loc)
		}
	}
	readBaseSalary(jp["baseSalary"], opp)
	if v, ok := jp["applicantLocationRequirements"]; ok && v != nil {
		ensureAttrs(opp)["applicant_location_requirements"] = v
	}
	if v := stringValue(jp["url"]); v != "" {
		opp.ApplyURL = v
	}
	return opp, nil
}

// readHiringOrganization tolerates both the canonical
// {"@type":"Organization","name":"..."} object form AND the looser
// string form some publishers emit ("hiringOrganization": "Acme Inc").
func readHiringOrganization(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case map[string]any:
		if name, ok := x["name"].(string); ok {
			return name
		}
	}
	return ""
}

// readOrganizationLogo extracts hiringOrganization.logo, tolerating both a bare
// URL string and a schema.org ImageObject ({"@type":"ImageObject","url":"..."}).
func readOrganizationLogo(v any) string {
	org, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	switch logo := org["logo"].(type) {
	case string:
		return logo
	case map[string]any:
		if u, ok := logo["url"].(string); ok {
			return u
		}
	}
	return ""
}

// readJobLocation extracts AnchorLocation.City + AnchorLocation.Country
// from a JobPosting.jobLocation block. Supports the single-object form
// and (best-effort) the first entry of an array form. Returns nil when
// nothing usable is present.
func readJobLocation(v any) *domain.Location {
	if v == nil {
		return nil
	}
	// JobPosting.jobLocation may be an array; pick the first object.
	if arr, ok := v.([]any); ok {
		if len(arr) == 0 {
			return nil
		}
		v = arr[0]
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	addr, ok := obj["address"].(map[string]any)
	if !ok {
		return nil
	}
	loc := &domain.Location{
		Country: stringValue(addr["addressCountry"]),
		City:    stringValue(addr["addressLocality"]),
		Region:  stringValue(addr["addressRegion"]),
	}
	return loc
}

// locationToText renders a Location as a human-readable string for
// LocationText. Empty inputs round-trip to an empty string.
func locationToText(l domain.Location) string {
	parts := make([]string, 0, 3)
	if l.City != "" {
		parts = append(parts, l.City)
	}
	if l.Region != "" {
		parts = append(parts, l.Region)
	}
	if l.Country != "" {
		parts = append(parts, l.Country)
	}
	return strings.Join(parts, ", ")
}

// readBaseSalary maps JobPosting.baseSalary into AmountMin / AmountMax
// / Currency. Both the nested QuantitativeValue object form and a flat
// scalar `value` are tolerated.
func readBaseSalary(v any, opp *domain.ExternalOpportunity) {
	if v == nil {
		return
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return
	}
	if cur := stringValue(obj["currency"]); cur != "" {
		opp.Currency = cur
	}
	switch val := obj["value"].(type) {
	case map[string]any:
		if min, ok := toFloat(val["minValue"]); ok {
			opp.AmountMin = min
		}
		if max, ok := toFloat(val["maxValue"]); ok {
			opp.AmountMax = max
		}
		if opp.AmountMin == 0 {
			if v, ok := toFloat(val["value"]); ok {
				opp.AmountMin = v
				opp.AmountMax = v
			}
		}
	case float64, json.Number, int, int64:
		if f, ok := toFloat(val); ok {
			opp.AmountMin = f
			opp.AmountMax = f
		}
	}
}

// stringValue coerces an arbitrary JSON value into a string. Slices
// fall through to the empty string — callers (notably AnchorLocation
// fields) treat missing/non-string values as unset.
func stringValue(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// toFloat coerces a JSON number, JSON.Number, or numeric-looking string
// into float64. Returns (0, false) when no usable number is present.
func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		if f, err := x.Float64(); err == nil {
			return f, true
		}
	case string:
		var f float64
		if _, err := fmt.Sscanf(x, "%f", &f); err == nil {
			return f, true
		}
	}
	return 0, false
}

// parseDate tolerates both the date-only form ("2026-05-28") and the
// full RFC3339 form ("2026-05-28T10:00:00Z"). Returns nil when no
// recognised layout matches.
func parseDate(s string) *time.Time {
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

// ensureAttrs lazy-initialises the Attributes map and returns it so
// callers can poke fields without nil checks.
func ensureAttrs(opp *domain.ExternalOpportunity) map[string]any {
	if opp.Attributes == nil {
		opp.Attributes = map[string]any{}
	}
	return opp.Attributes
}
