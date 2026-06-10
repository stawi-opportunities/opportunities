package recipe

import (
	"fmt"
	"strings"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

const recipeSynthInstructions = `You design DETERMINISTIC web-extraction recipes. Given sample pages from ONE
job/opportunity source, output a single JSON "recipe" that a non-AI executor
will run on every page of this source — so AI runs once, here, not per page.

Output ONLY the JSON recipe object. No prose, no code fences.

A recipe has this shape:
{
  "acquisition": "api" | "structured_data" | "selectors",
  "kind": {"mode":"source_default"|"fixed"|"by_path", "fixed":"<kind>", "path":<extractor>},
  "list": {"mode":"api"|"sitemap"|"structured_data"|"selector",
           "endpoint":"/path", "items_path":"$.jsonpath", "item_selector":".css",
           "link":<extractor>, "pagination":{"mode":"none"|"page_param"|"cursor"|"next_link","param":"page","next":<extractor>,"cursor":<extractor>,"max_pages":10}},
  "detail": {"record_source":"api"|"json_ld"|"next_data"|"microdata"|"html",
             "title":<extractor>, "description":<extractor>, "issuing_entity":<extractor>,
             "apply_url":<extractor>, "anchor_country":<extractor>, "location_text":<extractor>,
             "remote":<extractor>, "posted_at":<extractor>, "deadline":<extractor>,
             "amount_min":<extractor>, "amount_max":<extractor>, "currency":<extractor>,
             "categories":<extractor>, "company_logo_url":<extractor>, "company_profile":<extractor>,
             "attributes": {"<kind_attr>":<extractor>}}
}

An <extractor> resolves ONE value by trying sources in order (first non-empty wins):
{"from":["json_ld","next_data","microdata","selector","meta","record","const","page_url"],
 "json_path":"$.x",      // for json_ld/next_data/record
 "microdata":"itemprop", "selector":".css", "attr":"href",
 "meta":"og:image", "const":"literal",   // "page_url" yields the detail page's own URL
 "transform":["trim","lower","collapse_ws","html_to_text","absolute_url","parse_money","parse_date"]}

Rules:
- PREFER structured data: if pages embed schema.org JSON-LD (JobPosting etc.) or __NEXT_DATA__, map json_path; use CSS selectors only when needed.
- title, description, issuing_entity, apply_url, anchor_country are REQUIRED — always provide an extractor.
- apply_url: when no explicit apply link exists, the detail page itself is the application entry point — use {"from":["page_url"]} (often as the last fallback source).
- Dates: add "parse_date" transform. Money fields: "parse_money". Relative links: "absolute_url".
- Find the listing/detail structure: item_selector picks each card on the listing; link extracts each card's detail URL.
- Output VALID JSON that parses into the schema above.`

// buildGenerationPrompt assembles the synthesis prompt: instructions + target
// kind schema(s) + sample pages (truncated, structured-data preserved).
func (g *Generator) buildGenerationPrompt(src domain.Source, samples []SamplePage) string {
	var b strings.Builder
	b.WriteString(recipeSynthInstructions)

	b.WriteString("\n\nTarget opportunity kind(s) for THIS source and their field schemas:\n")
	for _, k := range []string(src.Kinds) {
		spec := g.reg.Resolve(k)
		fmt.Fprintf(&b, "\n--- kind=%s ---\n", k)
		if spec.ExtractionPrompt != "" {
			b.WriteString(spec.ExtractionPrompt)
			b.WriteString("\n")
		}
		if len(spec.KindRequired) > 0 {
			b.WriteString("Required attributes: " + strings.Join(spec.KindRequired, ", ") + "\n")
		}
		if len(spec.KindOptional) > 0 {
			b.WriteString("Optional attributes: " + strings.Join(spec.KindOptional, ", ") + "\n")
		}
	}

	b.WriteString("\nSample pages from this source:\n")
	for i, s := range samples {
		fmt.Fprintf(&b, "\n=== SAMPLE %d: %s ===\n", i+1, s.URL)
		b.WriteString(truncate(s.HTML, g.sampleChars))
		b.WriteString("\n")
	}
	return b.String()
}

func (g *Generator) repairPrompt(base, errMsg string) string {
	return base + "\n\nYour previous output was rejected: " + errMsg +
		"\nReturn a corrected JSON recipe that fixes this. Output ONLY the JSON."
}

func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n]
}
