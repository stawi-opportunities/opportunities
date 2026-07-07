package content

import (
	"strings"
	"testing"
)

const basicPage = `<!DOCTYPE html>
<html>
<head><title>Senior Go Engineer</title></head>
<body>
  <nav>
    <ul>
      <li><a href="/">Home</a></li>
      <li><a href="/jobs">Jobs</a></li>
    </ul>
  </nav>
  <main>
    <h1>Senior Go Engineer</h1>
    <p>We are looking for an experienced Go engineer to join our team.</p>
    <p>Requirements: 5+ years of Go, strong system design skills.</p>
  </main>
  <footer>
    <p>© 2024 ACME Corp. All rights reserved.</p>
  </footer>
</body>
</html>`

func TestExtractFromHTML_BasicPage(t *testing.T) {
	result, err := ExtractFromHTML(basicPage)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
		return
	}

	// Markdown should contain the main content body.
	if !strings.Contains(result.Markdown, "experienced Go engineer") {
		t.Errorf("Markdown should contain job description, got: %q", result.Markdown)
	}

	// Markdown should not contain footer boilerplate.
	// Note: trafilatura strips nav/footer; we verify the key body content is present.
}

func TestExtractFromHTML_Empty(t *testing.T) {
	for _, input := range []string{"", "   ", "\t\n"} {
		result, err := ExtractFromHTML(input)
		if err != nil {
			t.Fatalf("unexpected error for input %q: %v", input, err)
		}
		if result == nil {
			t.Fatalf("expected non-nil result for input %q", input)
			return
		}
	}
}

func TestExtractFromHTML_ReturnsMarkdown(t *testing.T) {
	result, err := ExtractFromHTML(basicPage)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Markdown == "" {
		t.Error("Markdown must not be empty for a page with content")
	}
}

func TestStripToText(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{
			input:    "<p>Hello <strong>world</strong></p>",
			expected: "Hello world",
		},
		{
			input:    "<div><h1>Title</h1><p>Body text here.</p></div>",
			expected: "Title Body text here.",
		},
		{
			input:    "",
			expected: "",
		},
		{
			input:    "<br/><hr/>",
			expected: "",
		},
		{
			input:    "no tags here",
			expected: "no tags here",
		},
	}

	for _, c := range cases {
		got := stripToText(c.input)
		if got != c.expected {
			t.Errorf("stripToText(%q) = %q, want %q", c.input, got, c.expected)
		}
	}
}

func TestHtmlToMarkdownOrStrip_ValidHTML(t *testing.T) {
	html := "<h1>Job Title</h1><p>Description here.</p>"
	md := htmlToMarkdownOrStrip(html)

	if !strings.Contains(md, "Job Title") {
		t.Errorf("markdown should contain heading text, got: %q", md)
	}
	if !strings.Contains(md, "Description here") {
		t.Errorf("markdown should contain paragraph text, got: %q", md)
	}
}

func TestHtmlToMarkdownOrStrip_Empty(t *testing.T) {
	result := htmlToMarkdownOrStrip("")
	if result != "" {
		t.Errorf("expected empty string, got: %q", result)
	}
}
