package recipe

import (
	"encoding/json"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// PageContext normalizes the four data planes a page exposes so a
// FieldExtractor can try them in order. Record is set only in api mode.
type PageContext struct {
	URL      string
	HTML     *goquery.Document
	JSONLD   []map[string]any
	NextData map[string]any
	Meta     map[string]string
	Record   map[string]any
	Tenant   string // current multi-tenant board token (api mode)
}

// NewPageContext parses html (may be empty for api mode) and harvests JSON-LD,
// __NEXT_DATA__-style state blobs, and meta tags. record is the API record in
// api mode (nil otherwise). Malformed embedded JSON is skipped, never fatal.
func NewPageContext(pageURL, html string, record map[string]any) (*PageContext, error) {
	pc := &PageContext{
		URL:    pageURL,
		Meta:   map[string]string{},
		Record: record,
	}

	if strings.TrimSpace(html) == "" {
		return pc, nil
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}
	pc.HTML = doc

	// meta: index by name OR property.
	doc.Find("meta").Each(func(_ int, s *goquery.Selection) {
		content, ok := s.Attr("content")
		if !ok {
			return
		}
		if name, ok := s.Attr("name"); ok {
			pc.Meta[name] = content
		}
		if prop, ok := s.Attr("property"); ok {
			pc.Meta[prop] = content
		}
	})

	// JSON-LD: every <script type="application/ld+json">. Objects are kept;
	// arrays and @graph wrappers are flattened to their object members.
	doc.Find(`script[type="application/ld+json"]`).Each(func(_ int, s *goquery.Selection) {
		pc.JSONLD = append(pc.JSONLD, parseJSONLDBlock(s.Text())...)
	})

	// __NEXT_DATA__ / __NUXT__-style state blob. Prefer the precise Next.js
	// id; fall back to the first parseable application/json for other
	// frameworks. This avoids grabbing an unrelated json blob (breadcrumb,
	// config) that happens to appear first.
	if s := doc.Find(`script#__NEXT_DATA__`).First(); s.Length() > 0 {
		var m map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(s.Text())), &m); err == nil && len(m) > 0 {
			pc.NextData = m
		}
	}
	if pc.NextData == nil {
		doc.Find(`script[type="application/json"]`).EachWithBreak(func(_ int, s *goquery.Selection) bool {
			var m map[string]any
			if err := json.Unmarshal([]byte(strings.TrimSpace(s.Text())), &m); err == nil && len(m) > 0 {
				pc.NextData = m
				return false
			}
			return true
		})
	}

	return pc, nil
}

// parseJSONLDBlock parses one <script> body into zero or more JSON-LD objects,
// flattening top-level arrays and a top-level "@graph" array.
func parseJSONLDBlock(body string) []map[string]any {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}
	var raw any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		// Real-world JSON-LD routinely embeds RAW control characters
		// (unescaped newlines/tabs inside description strings — myjobmag
		// serves them on every posting), which strict JSON rejects,
		// silently zeroing every json_ld extractor for the page. Retry
		// with control characters replaced by spaces: insignificant
		// outside strings, and inside strings the author meant
		// whitespace. Valid JSON is unaffected (its escapes are \-quoted
		// sequences, not raw bytes).
		cleaned := strings.Map(func(r rune) rune {
			if r < 0x20 {
				return ' '
			}
			return r
		}, body)
		if err := json.Unmarshal([]byte(cleaned), &raw); err != nil {
			return nil
		}
	}
	nodes := flattenJSONLD(raw)
	resolveGraphRefs(nodes)
	return nodes
}

// resolveGraphRefs replaces {"@id": ref} property values with the actual
// node carrying that @id, when both live in the same JSON-LD block. This
// is how schema.org commonly models relationships: a JobPosting's
// hiringOrganization is a bare {"@id": "…"} reference and the company
// name lives in a sibling Organization node (BrighterMonday, and many
// ATS-generated pages, do exactly this). Without resolution,
// $.hiringOrganization.name returns nothing. One level deep — enough for
// the envelope fields — and self-references are skipped to avoid cycles.
func resolveGraphRefs(nodes []map[string]any) {
	index := make(map[string]map[string]any, len(nodes))
	for _, n := range nodes {
		if id, ok := n["@id"].(string); ok && id != "" {
			index[id] = n
		}
	}
	if len(index) == 0 {
		return
	}
	deref := func(v any, parent map[string]any) any {
		m, ok := v.(map[string]any)
		if !ok {
			return v
		}
		id, ok := m["@id"].(string)
		if !ok || id == "" {
			return v
		}
		target, ok := index[id]
		if !ok || sameNode(target, parent) {
			return v
		}
		return target
	}
	for _, n := range nodes {
		for k, v := range n {
			switch val := v.(type) {
			case map[string]any:
				n[k] = deref(val, n)
			case []any:
				for i, item := range val {
					val[i] = deref(item, n)
				}
			}
		}
	}
}

func sameNode(a, b map[string]any) bool {
	ai, _ := a["@id"].(string)
	bi, _ := b["@id"].(string)
	return ai != "" && ai == bi
}

func flattenJSONLD(raw any) []map[string]any {
	var out []map[string]any
	switch v := raw.(type) {
	case map[string]any:
		if g, ok := v["@graph"].([]any); ok {
			for _, item := range g {
				out = append(out, flattenJSONLD(item)...)
			}
			return out // members are the useful objects; drop the @graph wrapper
		}
		out = append(out, v)
	case []any:
		for _, item := range v {
			out = append(out, flattenJSONLD(item)...)
		}
	}
	return out
}
