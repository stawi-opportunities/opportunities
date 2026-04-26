package eventsv1

import (
	"encoding/json"
	"testing"
	"time"
)

func TestVariantIngestedV1_RoundTrip(t *testing.T) {
	in := VariantIngestedV1{
		VariantID:     "var_1",
		SourceID:      "src_remoteok",
		ExternalID:    "abc",
		HardKey:       "src_remoteok|abc",
		Kind:          "job",
		Stage:         "ingested",
		Title:         "Senior Go Engineer",
		IssuingEntity: "Acme",
		AnchorCountry: "KE",
		AnchorCity:    "Nairobi",
		Currency:      "USD",
		AmountMin:     90000,
		AmountMax:     130000,
		Attributes:    map[string]any{"employment_type": "full-time", "seniority": "senior"},
		ScrapedAt:     time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC),
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out VariantIngestedV1
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Kind != "job" || out.Attributes["employment_type"] != "full-time" {
		t.Fatalf("round-trip lost data: %+v", out)
	}
}
