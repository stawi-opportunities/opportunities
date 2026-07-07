package service

import (
	"testing"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

func TestNormalizePreservesKindAttributes(t *testing.T) {
	out := Normalize(eventsv1.VariantIngestedV1{Kind: "scholarship", Title: " MSc Climate ", ApplyURL: " https://example.test/apply ", AnchorCountry: "ke", Attributes: map[string]any{"field_of_study": "Climate", "language": " EN ", "location_text": "Worldwide"}})
	if out.Attributes["field_of_study"] != "Climate" || out.Attributes["language"] != "en" || out.Attributes["country"] != "KE" || out.Attributes["remote_type"] != "remote" {
		t.Fatalf("unexpected normalized attributes: %#v", out.Attributes)
	}
	if out.ApplyURL != "https://example.test/apply" {
		t.Fatalf("ApplyURL = %q", out.ApplyURL)
	}
}
