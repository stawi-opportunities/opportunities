// Package publish prepares opportunity content for storage and display.
// Descriptions are stored as sanitized HTML so every client can render them
// the same way (no client-side markdown dialect divergence).
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
	// Goldmark with raw-HTML passthrough; bluemonday enforces the allow-list.
	mdRenderer = goldmark.New(goldmark.WithRendererOptions(html.WithUnsafe()))
	sanitizer  = bluemonday.UGCPolicy()
)

// DescriptionHTML converts a description — plain text, markdown, dirty HTML,
// or entity-encoded HTML — into sanitized HTML safe for storage and display.
// Empty input yields empty output.
//
// Sanitizer policy (UGC): keeps p, h1–h6, ul/ol/li, a, strong, em, code, pre,
// blockquote; strips script/style/event handlers/dangerous URL schemes.
// Idempotent on already-sanitized HTML.
func DescriptionHTML(in string) string {
	trimmed := strings.TrimSpace(in)
	if trimmed == "" {
		return ""
	}
	// Upstream connectors sometimes hand us entity-encoded HTML —
	// &lt;div class=&quot;…&quot;&gt; rather than <div class="…">.
	if containsEntities(trimmed) {
		decoded := stdhtml.UnescapeString(trimmed)
		if strings.HasPrefix(strings.TrimSpace(decoded), "<") {
			return sanitizer.Sanitize(decoded)
		}
		trimmed = decoded
		in = decoded
	}
	// Tag-prefixed input is treated as HTML (goldmark would escape tags).
	if strings.HasPrefix(strings.TrimSpace(trimmed), "<") {
		return sanitizer.Sanitize(in)
	}
	// Markdown or plain text → HTML, then sanitize.
	var buf bytes.Buffer
	if err := mdRenderer.Convert([]byte(in), &buf); err != nil {
		return sanitizer.Sanitize(in)
	}
	return sanitizer.Sanitize(buf.String())
}

func containsEntities(s string) bool {
	for _, tag := range []string{"&lt;", "&gt;", "&amp;", "&quot;", "&#"} {
		if strings.Contains(s, tag) {
			return true
		}
	}
	return false
}
