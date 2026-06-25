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
{"from":["json_ld","next_data","microdata","selector","xpath","meta","record","const","page_url"],
 "json_path":"$.x",      // for json_ld/next_data/record
 "microdata":"itemprop", "selector":".css", "xpath":"//h1", "attr":"href",
 "meta":"og:image", "const":"literal",   // "page_url" yields the detail page's own URL
 "transform":["trim","lower","collapse_ws","html_to_text","absolute_url","parse_money","parse_date"]}

Rules (prefer REUSABLE, standards-based extraction over brittle per-site classes; pick the most COMPLETE + FREE method first):
- METHOD ORDER: (1) official API if the board is an ATS (greenhouse/lever/workday — handled by connectors, not recipes). (2) list.mode="sitemap" when the site has a sitemap enumerating job URLs — set list.endpoint to the sitemap/sitemap-index URL (or leave empty to auto-discover from robots.txt) and list.link_pattern to the job-URL substring; this pulls the WHOLE board, not just listing page 1. (3) list.mode="selector" with link_pattern on the listing page. Only use scrape.do render for JS-only boards with no sitemap.
- LINK DISCOVERY: prefer list.link_pattern — the URL substring every job-detail page shares (e.g. "/listings/", "/job/", "/vacancy/"). The engine harvests every same-host link containing it. This is FAR more robust than item_selector+link CSS, which break on markup changes. Use item_selector+link only when no clean URL pattern exists.
- DETAIL: PREFER schema.org JSON-LD JobPosting (Google-for-Jobs makes it near-universal). Map the STANDARD property paths — $.title, $.description, $.hiringOrganization.name, $.datePosted, $.validThrough, $.jobLocation.address.addressCountry, $.jobLocation.address.addressLocality, $.baseSalary.value.minValue/maxValue, $.baseSalary.currency. These are identical across boards (reusable). hiringOrganization may be an @id reference — the engine resolves it, so $.hiringOrganization.name still works.
- FALLBACKS, in order: json_ld → meta (og:title/og:description) → microdata → css selector → xpath. Always give title/description a meta or selector fallback, since some postings omit JSON-LD.
- title, description, issuing_entity, apply_url, anchor_country are REQUIRED.
- apply_url: when no explicit apply link exists, use {"from":["page_url"]} (last fallback).
- Dates: "parse_date". Money: "parse_money". Relative links: "absolute_url".
- Output VALID JSON that parses into the schema above.`

// buildGenerationPrompt assembles the synthesis prompt: instructions + target
// kind schema(s) + the LISTING page + detail sample pages (truncated,
// structured-data preserved). The listing section exists because
// list.item_selector / list.link / pagination can only be derived from the
// listing markup — detail samples alone forced the LLM to invent them.
func (g *Generator) buildGenerationPrompt(src domain.Source, listing SamplePage, samples []SamplePage) string {
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

	if listing.HTML != "" {
		fmt.Fprintf(&b, "\n=== LISTING PAGE: %s ===\n", listing.URL)
		b.WriteString("Derive list.item_selector, list.link and list.pagination from THIS page's markup — the repeated job-card elements below are what the executor will iterate:\n")
		b.WriteString(truncate(listing.HTML, g.sampleChars))
		b.WriteString("\n")
	}

	b.WriteString("\nDetail sample pages from this source:\n")
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
