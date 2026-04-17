package publish

import (
	"bytes"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/renderer/html"
)

var (
	// Goldmark with raw-HTML passthrough enabled; bluemonday handles safety.
	mdRenderer = goldmark.New(goldmark.WithRendererOptions(html.WithUnsafe()))
	sanitizer  = bluemonday.UGCPolicy()
)

// RenderDescriptionHTML converts a job description — which may be plain text,
// markdown, or already-dirty HTML — into sanitized HTML safe to inject via
// innerHTML. Empty input yields empty output. The sanitizer uses bluemonday's
// UGC policy (keeps p, h1-h6, ul/ol/li, a, strong, em, code, pre, blockquote;
// strips script/style/event handlers/dangerous URL schemes).
func RenderDescriptionHTML(in string) string {
	trimmed := strings.TrimSpace(in)
	if trimmed == "" {
		return ""
	}
	// Inputs that begin with a tag are treated as HTML directly — goldmark's
	// default behaviour is to escape stray HTML, which would hide content from
	// the sanitizer. For mixed content, html.WithUnsafe() keeps tags intact.
	if strings.HasPrefix(trimmed, "<") {
		return sanitizer.Sanitize(in)
	}
	var buf bytes.Buffer
	if err := mdRenderer.Convert([]byte(in), &buf); err != nil {
		return sanitizer.Sanitize(in)
	}
	return sanitizer.Sanitize(buf.String())
}
