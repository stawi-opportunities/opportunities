package content

import (
	"html"
	"regexp"
	"strings"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/go-shiori/dom"
	"github.com/markusmobius/go-trafilatura"
)

// Extracted holds the three forms of extracted content.
type Extracted struct {
	RawHTML   string // original HTTP response body
	CleanHTML string // main content extracted (nav/ads/footer removed)
	Markdown  string // clean markdown suitable for AI and humans
}

var metaTagRe = regexp.MustCompile(`(?is)<meta\s[^>]*>`)
var contentAttrRe = regexp.MustCompile(`(?is)content\s*=\s*["']([^"']+)["']`)

// OGImage returns the page's Open Graph image (og:image / og:image:secure_url),
// falling back to twitter:image — typically the company/share image on a job or
// company page, used as a company-logo fallback when structured data has none.
// Returns "" when absent. Tolerant of attribute order and quote style; only
// http(s) URLs are accepted.
func OGImage(rawHTML string) string {
	if rawHTML == "" {
		return ""
	}
	var twitter string
	for _, tag := range metaTagRe.FindAllString(rawHTML, -1) {
		lt := strings.ToLower(tag)
		isOG := strings.Contains(lt, "og:image")
		isTwitter := strings.Contains(lt, "twitter:image")
		if !isOG && !isTwitter {
			continue
		}
		m := contentAttrRe.FindStringSubmatch(tag)
		if len(m) != 2 {
			continue
		}
		u := strings.TrimSpace(html.UnescapeString(m[1]))
		if !strings.HasPrefix(strings.ToLower(u), "http") {
			continue
		}
		if isOG {
			return u // prefer og:image
		}
		if twitter == "" {
			twitter = u
		}
	}
	return twitter
}

// ExtractFromHTML takes raw HTML from an HTTP response, uses go-trafilatura to
// extract the main content, converts it to Markdown, and returns all three forms.
func ExtractFromHTML(rawHTML string) (*Extracted, error) {
	if strings.TrimSpace(rawHTML) == "" {
		return &Extracted{}, nil
	}

	result := &Extracted{
		RawHTML: rawHTML,
	}

	// Use trafilatura to extract main content.
	opts := trafilatura.Options{
		IncludeLinks:  true,
		IncludeImages: false,
	}

	extracted, err := trafilatura.Extract(strings.NewReader(rawHTML), opts)
	if err != nil || extracted == nil {
		// Fallback: use the raw HTML as the clean HTML.
		result.CleanHTML = rawHTML
		result.Markdown = htmlToMarkdownOrStrip(rawHTML)
		return result, nil
	}

	// Render ContentNode back to an HTML string.
	if extracted.ContentNode != nil {
		result.CleanHTML = dom.OuterHTML(extracted.ContentNode)
	} else {
		result.CleanHTML = extracted.ContentText
	}

	// Convert clean HTML to Markdown.
	if result.CleanHTML != "" {
		result.Markdown = htmlToMarkdownOrStrip(result.CleanHTML)
	} else if extracted.ContentText != "" {
		result.Markdown = extracted.ContentText
	}

	return result, nil
}

// ExtractFromJSON handles JSON API connectors that already have structured data.
// The JSON payload is stored as RawHTML, CleanHTML is empty, and Markdown is
// derived from the description HTML (if provided).
func ExtractFromJSON(jsonResponse string, description string) *Extracted {
	result := &Extracted{
		RawHTML:   jsonResponse,
		CleanHTML: "",
	}

	if strings.TrimSpace(description) != "" {
		result.Markdown = htmlToMarkdownOrStrip(description)
	}

	return result
}

// htmlToMarkdownOrStrip converts an HTML string to Markdown. If conversion
// fails it falls back to stripToText.
func htmlToMarkdownOrStrip(htmlContent string) string {
	if strings.TrimSpace(htmlContent) == "" {
		return ""
	}

	md, err := htmltomarkdown.ConvertString(htmlContent)
	if err != nil {
		return stripToText(htmlContent)
	}

	return md
}

// ToCleanText converts a description fragment that may be raw HTML, or
// entity-escaped HTML, into clean Markdown suitable for both human display
// and the search snippet. ATS APIs (greenhouse j.Content), feeds, and
// JSON-LD descriptions arrive as HTML — sometimes DOUBLE-encoded, e.g.
// "&lt;div class=&quot;content-intro&quot;&gt;" — and were previously stored
// verbatim, so the UI showed literal tags / entities.
//
// Steps: (1) HTML-unescape so double-encoded markup becomes real tags
// ("&lt;div&gt;" -> "<div>"); (2) if the result still contains tags, convert
// to Markdown (falling back to a plain-text strip on failure); (3) input that
// is already plain text passes through untouched. Idempotent: re-running on
// already-clean Markdown leaves it unchanged.
func ToCleanText(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	decoded := html.UnescapeString(s)
	if !strings.Contains(decoded, "<") {
		return strings.TrimSpace(decoded)
	}
	return strings.TrimSpace(htmlToMarkdownOrStrip(decoded))
}

// stripToText removes all HTML tags and collapses whitespace, providing a plain
// text fallback when Markdown conversion is not possible.
var tagRe = regexp.MustCompile(`<[^>]+>`)

func stripToText(htmlContent string) string {
	plain := tagRe.ReplaceAllString(htmlContent, " ")
	// Collapse runs of whitespace.
	plain = strings.Join(strings.Fields(plain), " ")
	return plain
}
