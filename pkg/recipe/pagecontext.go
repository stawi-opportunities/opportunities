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
		for _, obj := range parseJSONLDBlock(s.Text()) {
			pc.JSONLD = append(pc.JSONLD, obj)
		}
	})

	// __NEXT_DATA__ / __NUXT__-style state blob (first parseable wins).
	doc.Find(`script[type="application/json"]`).EachWithBreak(func(_ int, s *goquery.Selection) bool {
		var m map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(s.Text())), &m); err == nil && len(m) > 0 {
			pc.NextData = m
			return false
		}
		return true
	})

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
		return nil
	}
	return flattenJSONLD(raw)
}

func flattenJSONLD(raw any) []map[string]any {
	var out []map[string]any
	switch v := raw.(type) {
	case map[string]any:
		if g, ok := v["@graph"].([]any); ok {
			for _, item := range g {
				out = append(out, flattenJSONLD(item)...)
			}
		}
		out = append(out, v)
	case []any:
		for _, item := range v {
			out = append(out, flattenJSONLD(item)...)
		}
	}
	return out
}
