package spec_test

import (
	"strings"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/connectors/spec"
)

func TestParseSpec_Shorthand(t *testing.T) {
	body := []byte(`
type: htmllisting
list_url: https://example.com/jobs
fields:
  title: ".title::text"
`)
	s, err := spec.ParseSpec(body)
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	if got := s.Fields["title"].Selector; got != ".title::text" {
		t.Fatalf("shorthand selector = %q; want %q", got, ".title::text")
	}
	if got := s.Fields["title"].ParseAs; got != "" {
		t.Fatalf("shorthand parse_as = %q; want empty", got)
	}
}

func TestParseSpec_StructForm(t *testing.T) {
	body := []byte(`
type: htmllisting
list_url: https://example.com/jobs
fields:
  posted_at:
    selector: ".posted::text"
    parse_as: relative_time
`)
	s, err := spec.ParseSpec(body)
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	if got := s.Fields["posted_at"].Selector; got != ".posted::text" {
		t.Fatalf("struct selector = %q; want %q", got, ".posted::text")
	}
	if got := s.Fields["posted_at"].ParseAs; got != "relative_time" {
		t.Fatalf("struct parse_as = %q; want %q", got, "relative_time")
	}
}

func TestValidate_MissingListURL(t *testing.T) {
	body := []byte(`
type: htmllisting
fields:
  title: ".title"
`)
	if _, err := spec.ParseSpec(body); err == nil {
		t.Fatal("expected error for missing list_url")
	}
}

func TestValidate_UnknownType(t *testing.T) {
	body := []byte(`
type: gibberish
list_url: https://x
fields:
  title: ".title"
`)
	_, err := spec.ParseSpec(body)
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
	if !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("error = %v; want substring 'unknown'", err)
	}
}

func TestParseSpec_KnownTypeAccepted(t *testing.T) {
	cases := []struct {
		name    string
		typeStr string
		extra   string
	}{
		{"htmllisting", "htmllisting", "fields:\n  title: .t\n"},
		{"jsonfeed", "jsonfeed", "fields:\n  title: $.t\n"},
		{"rssfeed", "rssfeed", "fields:\n  title: title\n"},
		{"sitemap", "sitemap", ""}, // sitemap has no required fields
		{"schemaorgjsonld", "schemaorgjsonld", "fields:\n  title: title\n"},
		{"xmlfeed", "xmlfeed", "fields:\n  title: title/text()\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte("type: " + tc.typeStr + "\nlist_url: https://x\n" + tc.extra)
			s, err := spec.ParseSpec(body)
			if err != nil {
				t.Fatalf("ParseSpec(%s): %v", tc.typeStr, err)
			}
			if string(s.Type) != tc.typeStr {
				t.Fatalf("Type = %q; want %q", s.Type, tc.typeStr)
			}
		})
	}
}

func TestNewFromYAML_NoImpl(t *testing.T) {
	// Slice 1 ships only the dispatch layer; no Impl packages are
	// imported here, so every known type lacks a registered Impl and
	// NewFromYAML must surface that as an error.
	body := []byte(`
type: htmllisting
list_url: https://example.com/jobs
fields:
  title: ".title"
`)
	_, err := spec.NewFromYAML("example", body, nil)
	if err == nil {
		t.Fatal("expected error when no impl is registered")
	}
	if !strings.Contains(err.Error(), "no impl registered") {
		t.Fatalf("error = %v; want substring 'no impl registered'", err)
	}
}
