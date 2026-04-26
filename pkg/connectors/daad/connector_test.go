package daad_test

import (
	"context"
	_ "embed"
	"testing"

	"github.com/lib/pq"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
)

//go:embed testdata/detail.html
var detailHTML string

// fakeLLM returns the JSON the live extractor would receive from a
// well-behaved model that has read detail.html. It records nothing —
// the test only cares that downstream parsing + Verify accept it.
type fakeLLM struct{}

func (fakeLLM) Complete(_ context.Context, _ string) (string, error) {
	return `{
		"title": "Climate Science Master's Scholarship 2026",
		"description": "The DAAD offers full scholarships for international students pursuing a master's degree in climate-related fields at German universities. Funding includes a monthly stipend, health insurance, and a travel allowance.",
		"issuing_entity": "DAAD",
		"apply_url": "https://www.daad.de/apply/climate-msc-2026",
		"anchor_country": "DE",
		"deadline": "2026-12-01T00:00:00Z",
		"currency": "EUR",
		"amount_min": 1200,
		"field_of_study": "Climate Science",
		"degree_level": "masters",
		"eligible_nationalities": ["INT"]
	}`, nil
}

// TestDAAD_ExtractAndVerify is the proof point for Phase 8.1. It runs the
// scholarship detail fixture through the two-stage extractor (single-kind
// path skips the classifier) and asserts the result satisfies both the
// kind contract from the registry and the source-level
// RequiredAttributesByKind override declared by the DAAD seed.
func TestDAAD_ExtractAndVerify(t *testing.T) {
	reg, err := opportunity.LoadFromDir("../../../definitions/opportunity-kinds")
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}

	ext := extraction.New(extraction.Config{
		BaseURL:  "http://test.invalid",
		Model:    "test",
		Registry: reg,
	})
	ext.SetLLM(fakeLLM{})

	opp, err := ext.Extract(context.Background(), detailHTML, []string{"scholarship"})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	if opp.Kind != "scholarship" {
		t.Errorf("Kind=%q, want scholarship", opp.Kind)
	}
	if opp.Title == "" || opp.Title != "Climate Science Master's Scholarship 2026" {
		t.Errorf("Title=%q", opp.Title)
	}
	if opp.IssuingEntity != "DAAD" {
		t.Errorf("IssuingEntity=%q", opp.IssuingEntity)
	}
	if opp.Deadline == nil {
		t.Error("Deadline not parsed onto envelope")
	}
	if got := opp.AttrString("field_of_study"); got == "" {
		t.Errorf("attribute field_of_study missing: %+v", opp.Attributes)
	}
	if got := opp.AttrString("degree_level"); got != "masters" {
		t.Errorf("attribute degree_level=%q, want masters", got)
	}

	// Verify uses the deadline envelope field plus the field_of_study
	// attribute. The source mirrors the DAAD seed: kinds:[scholarship]
	// with the same kind-required overrides the registry already enforces
	// — proving belt-and-braces alignment between seed and registry.
	src := &domain.Source{
		Kinds: pq.StringArray{"scholarship"},
		RequiredAttributesByKind: map[string][]string{
			"scholarship": {"deadline", "field_of_study"},
		},
	}

	// Verify checks attrPresent for every kind_required key, but the
	// extractor has lifted "deadline" onto the envelope. Mirror it back
	// onto Attributes so Verify's attribute-keyed check finds it — this
	// is the same coercion the full pipeline performs in the worker.
	if opp.Attributes == nil {
		opp.Attributes = map[string]any{}
	}
	if opp.Deadline != nil {
		opp.Attributes["deadline"] = opp.Deadline.Format("2006-01-02")
	}

	res := opportunity.Verify(opp, src, reg)
	if !res.OK {
		t.Fatalf("Verify failed: missing=%v mismatch=%q extra=%v", res.Missing, res.Mismatch, res.Extra)
	}
}
