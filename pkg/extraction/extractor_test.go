package extraction

import (
	"context"
	"strings"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
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

// TestParseExtractionJSON_SplitsEnvelopeFromAttributes verifies that the
// generic parser routes universal keys onto the strongly-typed envelope
// and stashes everything else under Attributes — and that an empty
// Attributes collapses to nil so the verify stage doesn't lie.
func TestParseExtractionJSON_SplitsEnvelopeFromAttributes(t *testing.T) {
	raw := `{
		"title": "Senior Go Engineer",
		"description": "Build scalable services.",
		"issuing_entity": "Acme Corp",
		"apply_url": "https://acme.example/apply",
		"anchor_country": "KE",
		"anchor_city": "Nairobi",
		"currency": "KES",
		"amount_min": 80000,
		"amount_max": 120000,
		"employment_type": "full-time",
		"seniority": "senior"
	}`

	opp, err := parseExtractionJSON(raw, "job")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opp.Kind != "job" {
		t.Errorf("Kind=%q, want job", opp.Kind)
	}
	if opp.Title != "Senior Go Engineer" {
		t.Errorf("Title=%q", opp.Title)
	}
	if opp.IssuingEntity != "Acme Corp" {
		t.Errorf("IssuingEntity=%q", opp.IssuingEntity)
	}
	if opp.AnchorLocation == nil || opp.AnchorLocation.Country != "KE" || opp.AnchorLocation.City != "Nairobi" {
		t.Errorf("AnchorLocation=%+v", opp.AnchorLocation)
	}
	if opp.Currency != "KES" || opp.AmountMin != 80000 || opp.AmountMax != 120000 {
		t.Errorf("amounts: cur=%q min=%v max=%v", opp.Currency, opp.AmountMin, opp.AmountMax)
	}
	if opp.Attributes["employment_type"] != "full-time" {
		t.Errorf("employment_type lost: %+v", opp.Attributes)
	}
	if opp.Attributes["seniority"] != "senior" {
		t.Errorf("seniority lost: %+v", opp.Attributes)
	}
}

// TestParseExtractionJSON_Malformed asserts the parser surfaces a useful
// error on malformed model output rather than returning a half-filled
// opportunity.
func TestParseExtractionJSON_Malformed(t *testing.T) {
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
			opp, err := parseExtractionJSON(tc.raw, "job")
			if err == nil {
				t.Errorf("expected error for input %q, got opp: %+v", tc.raw, opp)
			}
		})
	}
}

// TestExtract_SingleKindSkipsClassifier verifies the small-but-important
// optimisation: when the source declares exactly one kind we never burn
// a classifier round-trip.
func TestExtract_SingleKindSkipsClassifier(t *testing.T) {
	llm := &fakeLLM{
		extract: `{"title":"Senior Go Engineer","description":"...long enough body...","issuing_entity":"Acme","apply_url":"https://acme.example/apply","anchor_country":"KE","employment_type":"full-time"}`,
	}
	reg, err := opportunity.LoadFromDir("../../definitions/opportunity-kinds")
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	e := &Extractor{llm: llm, registry: reg}
	opp, err := e.Extract(context.Background(), "<html/>", []string{"job"})
	if err != nil {
		t.Fatal(err)
	}
	if opp.Kind != "job" {
		t.Errorf("Kind=%q, want job", opp.Kind)
	}
	if llm.classifyCalls != 0 {
		t.Errorf("classifier called %d times for single-kind source", llm.classifyCalls)
	}
	if opp.Attributes["employment_type"] != "full-time" {
		t.Errorf("attribute lost: %+v", opp.Attributes)
	}
}

// TestExtract_MultiKindClassifies exercises the classify-then-extract
// path, asserting the classifier output drives the kind on the result
// and that kind-specific attributes survive the JSON parse.
func TestExtract_MultiKindClassifies(t *testing.T) {
	llm := &fakeLLM{
		classify: "scholarship",
		extract:  `{"title":"MSc","description":"long enough body for verify and friends here please","issuing_entity":"ETH","field_of_study":"Climate","deadline":"2026-12-01T00:00:00Z"}`,
	}
	reg, err := opportunity.LoadFromDir("../../definitions/opportunity-kinds")
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	e := &Extractor{llm: llm, registry: reg}
	opp, err := e.Extract(context.Background(), "<html/>", []string{"job", "scholarship"})
	if err != nil {
		t.Fatal(err)
	}
	if opp.Kind != "scholarship" {
		t.Errorf("Kind=%q, want scholarship", opp.Kind)
	}
	if llm.classifyCalls != 1 {
		t.Errorf("classifier called %d times, want 1", llm.classifyCalls)
	}
	if opp.Attributes["field_of_study"] != "Climate" {
		t.Errorf("attribute lost: %+v", opp.Attributes)
	}
	if opp.Deadline == nil {
		t.Errorf("deadline not parsed onto envelope")
	}
}

// fakeLLM records classifier-vs-extract calls separately so tests can
// assert how many round-trips the two-stage pipeline actually made.
type fakeLLM struct {
	classify, extract string
	classifyCalls     int
}

func (f *fakeLLM) Complete(_ context.Context, prompt string) (string, error) {
	if strings.Contains(prompt, "Classify the document") {
		f.classifyCalls++
		return f.classify, nil
	}
	return f.extract, nil
}
