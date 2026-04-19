package publish

import (
	"bytes"
	stdhtml "html"
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
// markdown, already-dirty HTML, or entity-encoded HTML — into sanitized HTML
// safe to inject via innerHTML. Empty input yields empty output. The
// sanitizer uses bluemonday's UGC policy (keeps p, h1-h6, ul/ol/li, a,
// strong, em, code, pre, blockquote; strips script/style/event handlers/
// dangerous URL schemes).
func RenderDescriptionHTML(in string) string {
	trimmed := strings.TrimSpace(in)
	if trimmed == "" {
		return ""
	}
	// Upstream connectors sometimes hand us entity-encoded HTML —
	// &lt;div class=&quot;…&quot;&gt; rather than <div class="…">.
	// Without decoding, the "starts with <" branch misses and the
	// whole thing gets wrapped in a <p>, then rendered literally.
	// html.UnescapeString is a no-op on plain text (no entities).
	if containsEntities(trimmed) {
		decoded := stdhtml.UnescapeString(trimmed)
		if strings.HasPrefix(strings.TrimSpace(decoded), "<") {
			return sanitizer.Sanitize(decoded)
		}
		trimmed = decoded
		in = decoded
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

// containsEntities is a cheap pre-check: we only decode when the input
// looks entity-encoded. Skips the UnescapeString cost on the 99% of
// jobs that are already plain / HTML.
func containsEntities(s string) bool {
	for _, tag := range []string{"&lt;", "&gt;", "&amp;", "&quot;", "&#"} {
		if strings.Contains(s, tag) {
			return true
		}
	}
	return false
}
