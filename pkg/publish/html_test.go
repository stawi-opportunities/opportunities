package publish_test

import (
	"strings"
	"testing"

	"stawi.jobs/pkg/publish"
)

func TestRenderDescriptionHTML_StripsScript(t *testing.T) {
	in := `<p>Hello</p><script>alert(1)</script>`
	out := publish.RenderDescriptionHTML(in)
	if strings.Contains(out, "<script") {
		t.Fatalf("script tag not stripped: %q", out)
	}
	if !strings.Contains(out, "Hello") {
		t.Fatalf("content lost: %q", out)
	}
}

func TestRenderDescriptionHTML_KeepsSafeMarkdown(t *testing.T) {
	in := "# Heading\n\nHere is a **bold** word and a [link](https://example.com)."
	out := publish.RenderDescriptionHTML(in)
	for _, want := range []string{"<h1", "<strong>", `<a `, `href="https://example.com"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

func TestRenderDescriptionHTML_PlainText(t *testing.T) {
	in := "A plain paragraph without any markdown."
	out := publish.RenderDescriptionHTML(in)
	if !strings.Contains(out, "<p>A plain paragraph") {
		t.Errorf("plain text should wrap in <p>, got %q", out)
	}
}

func TestRenderDescriptionHTML_Empty(t *testing.T) {
	if out := publish.RenderDescriptionHTML(""); out != "" {
		t.Errorf("empty in → empty out, got %q", out)
	}
}

func TestRenderDescriptionHTML_StripsOnclick(t *testing.T) {
	in := `<a href="https://x.test" onclick="bad()">click</a>`
	out := publish.RenderDescriptionHTML(in)
	if strings.Contains(out, "onclick") {
		t.Fatalf("onclick not stripped: %q", out)
	}
}

func TestRenderDescriptionHTML_StripsJavaScriptScheme(t *testing.T) {
	in := `<a href="javascript:bad()">click</a>`
	out := publish.RenderDescriptionHTML(in)
	if strings.Contains(strings.ToLower(out), "javascript:") {
		t.Fatalf("javascript scheme not stripped: %q", out)
	}
}
