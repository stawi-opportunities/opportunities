package extraction

import (
	"testing"
)

func TestStripHTML(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "removes simple tags",
			input: "<h1>Senior Engineer</h1><p>Join our team.</p>",
			want:  "Senior Engineer Join our team.",
		},
		{
			name:  "removes script blocks",
			input: `<p>Hello</p><script type="text/javascript">var x = 1;</script><p>World</p>`,
			want:  "Hello World",
		},
		{
			name:  "removes style blocks",
			input: `<style>.foo{color:red}</style><p>Content</p>`,
			want:  "Content",
		},
		{
			name:  "decodes html entities",
			input: "<p>Salary &amp; Benefits &lt;great&gt;</p>",
			want:  "Salary & Benefits <great>",
		},
		{
			name:  "collapses whitespace",
			input: "<p>  too   many   spaces  </p>",
			want:  "too many spaces",
		},
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := stripHTML(tc.input)
			if got != tc.want {
				t.Errorf("stripHTML(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestTruncateText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		n     int
		want  string
	}{
		{
			name:  "truncates long string",
			input: "abcdefghij",
			n:     5,
			want:  "abcde",
		},
		{
			name:  "does not truncate short string",
			input: "abc",
			n:     10,
			want:  "abc",
		},
		{
			name:  "exact length is unchanged",
			input: "abcde",
			n:     5,
			want:  "abcde",
		},
		{
			name:  "handles unicode runes correctly",
			input: "こんにちは世界",
			n:     5,
			want:  "こんにちは",
		},
		{
			name:  "empty input",
			input: "",
			n:     10,
			want:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateText(tc.input, tc.n)
			if got != tc.want {
				t.Errorf("truncateText(%q, %d) = %q, want %q", tc.input, tc.n, got, tc.want)
			}
		})
	}
}

func TestParseResponse_ValidJSON(t *testing.T) {
	raw := `{
		"title": "Software Engineer",
		"company": "Acme Corp",
		"location": "Nairobi, Kenya",
		"description": "Build scalable services.",
		"apply_url": "https://acme.com/apply",
		"employment_type": "full-time",
		"remote_type": "hybrid",
		"salary_min": "80000",
		"salary_max": "120000",
		"currency": "KES"
	}`

	fields, err := parseResponse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fields.Title != "Software Engineer" {
		t.Errorf("Title = %q, want %q", fields.Title, "Software Engineer")
	}
	if fields.Company != "Acme Corp" {
		t.Errorf("Company = %q, want %q", fields.Company, "Acme Corp")
	}
	if fields.Location != "Nairobi, Kenya" {
		t.Errorf("Location = %q, want %q", fields.Location, "Nairobi, Kenya")
	}
	if fields.Currency != "KES" {
		t.Errorf("Currency = %q, want %q", fields.Currency, "KES")
	}
}

func TestParseResponse_MalformedJSON(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{name: "empty string", raw: ""},
		{name: "plain text", raw: "here is the job posting info"},
		{name: "truncated json", raw: `{"title": "Engineer"`},
		{name: "array instead of object", raw: `["title", "company"]`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fields, err := parseResponse(tc.raw)
			if err == nil {
				t.Errorf("expected error for input %q, got fields: %+v", tc.raw, fields)
			}
		})
	}
}
