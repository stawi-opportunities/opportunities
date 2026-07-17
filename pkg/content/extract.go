package content

import (
	"html"
	"regexp"
	"strings"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/go-shiori/dom"
	"github.com/markusmobius/go-trafilatura"
)

// Extracted holds parsed content used by extraction.
// HTML is the preferred body form (sanitized later at normalize via
// publish.DescriptionHTML). Markdown is retained for callers that still
// want a text-oriented view of the same extract.
type Extracted struct {
	HTML     string
	Markdown string
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
// extract the main content and converts it to Markdown. The response body is
// not retained in the result.
func ExtractFromHTML(rawHTML string) (*Extracted, error) {
	if strings.TrimSpace(rawHTML) == "" {
		return &Extracted{}, nil
	}

	result := &Extracted{}

	// Use trafilatura to extract main content.
	opts := trafilatura.Options{
		IncludeLinks:  true,
		IncludeImages: false,
	}

	extracted, err := trafilatura.Extract(strings.NewReader(rawHTML), opts)
	if err != nil || extracted == nil {
		// Fallback: use the raw HTML body.
		result.HTML = strings.TrimSpace(rawHTML)
		result.Markdown = htmlToMarkdownOrStrip(rawHTML)
		return result, nil
	}

	// Prefer main-content HTML; keep markdown as a secondary text view.
	if extracted.ContentNode != nil {
		htmlBody := dom.OuterHTML(extracted.ContentNode)
		result.HTML = strings.TrimSpace(htmlBody)
		result.Markdown = htmlToMarkdownOrStrip(htmlBody)
	} else {
		result.HTML = ""
		result.Markdown = extracted.ContentText
	}

	return result, nil
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
// entity-escaped HTML, into plain text (tags stripped). Prefer
// publish.DescriptionHTML for storage/display; use this for embeddings,
// language detection, and search snippets.
func ToCleanText(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	decoded := html.UnescapeString(s)
	if strings.Contains(decoded, "<") {
		return strings.TrimSpace(stripToText(decoded))
	}
	return strings.TrimSpace(decoded)
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
