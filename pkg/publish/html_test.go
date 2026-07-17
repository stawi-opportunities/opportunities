package publish_test

import (
	"strings"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/publish"
)

func TestDescriptionHTML_StripsScript(t *testing.T) {
	in := `<p>Hello</p><script>alert(1)</script>`
	out := publish.DescriptionHTML(in)
	if strings.Contains(out, "<script") {
		t.Fatalf("script tag not stripped: %q", out)
	}
	if !strings.Contains(out, "Hello") {
		t.Fatalf("content lost: %q", out)
	}
}

func TestDescriptionHTML_KeepsSafeMarkdown(t *testing.T) {
	in := "# Heading\n\nHere is a **bold** word and a [link](https://example.com)."
	out := publish.DescriptionHTML(in)
	for _, want := range []string{"<h1", "<strong>", `<a `, `href="https://example.com"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

func TestDescriptionHTML_PlainText(t *testing.T) {
	in := "A plain paragraph without any markdown."
	out := publish.DescriptionHTML(in)
	if !strings.Contains(out, "<p>A plain paragraph") {
		t.Errorf("plain text should wrap in <p>, got %q", out)
	}
}

func TestDescriptionHTML_Empty(t *testing.T) {
	if out := publish.DescriptionHTML(""); out != "" {
		t.Errorf("empty in → empty out, got %q", out)
	}
}

func TestDescriptionHTML_StripsOnclick(t *testing.T) {
	in := `<a href="https://x.test" onclick="bad()">click</a>`
	out := publish.DescriptionHTML(in)
	if strings.Contains(out, "onclick") {
		t.Fatalf("onclick not stripped: %q", out)
	}
}

func TestDescriptionHTML_StripsJavaScriptScheme(t *testing.T) {
	in := `<a href="javascript:bad()">click</a>`
	out := publish.DescriptionHTML(in)
	if strings.Contains(strings.ToLower(out), "javascript:") {
		t.Fatalf("javascript scheme not stripped: %q", out)
	}
}

func TestDescriptionHTML_EntityEncodedHTML(t *testing.T) {
	in := `&lt;div class=&#34;content-intro&#34;&gt;&lt;p&gt;Stripe is a financial platform.&lt;/p&gt;&lt;/div&gt;`
	out := publish.DescriptionHTML(in)
	if strings.Contains(out, "&lt;div") || strings.Contains(out, "&#34;") {
		t.Errorf("entities not decoded: %q", out)
	}
	if !strings.Contains(out, "<div") || !strings.Contains(out, "<p>") {
		t.Errorf("decoded HTML not rendered: %q", out)
	}
	if strings.Contains(out, "<script") {
		t.Errorf("sanitizer bypassed: %q", out)
	}
}

func TestDescriptionHTML_EntityEncodedScriptStillStripped(t *testing.T) {
	in := `&lt;p&gt;hello&lt;/p&gt;&lt;script&gt;bad()&lt;/script&gt;`
	out := publish.DescriptionHTML(in)
	if strings.Contains(out, "<script") {
		t.Errorf("script survived entity-decode+sanitize: %q", out)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("content lost: %q", out)
	}
}

func TestDescriptionHTML_IdempotentOnSanitized(t *testing.T) {
	in := `<p>Once <strong>clean</strong>.</p>`
	once := publish.DescriptionHTML(in)
	twice := publish.DescriptionHTML(once)
	if once != twice {
		t.Fatalf("not idempotent:\n  once=%q\n twice=%q", once, twice)
	}
}
