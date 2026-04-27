package opportunity

import (
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

func newRegistry(t *testing.T) *Registry {
	t.Helper()
	reg, err := LoadFromDir("../../definitions/opportunity-kinds")
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestVerify_MissingTitle(t *testing.T) {
	reg := newRegistry(t)
	src := &domain.Source{Kinds: []string{"job"}}
	opp := &domain.ExternalOpportunity{Kind: "job", Description: "long enough description here for sure"}
	r := Verify(opp, src, reg)
	if r.OK || !contains(r.Missing, "title") {
		t.Fatalf("expected Missing to include 'title', got %+v", r)
	}
}

func TestVerify_KindNotInSourceContract(t *testing.T) {
	reg := newRegistry(t)
	src := &domain.Source{Kinds: []string{"job"}}
	opp := &domain.ExternalOpportunity{
		Kind:          "scholarship",
		Title:         "MSc Climate Science",
		Description:   "Long description that is well above the minimum length required for verify to be happy.",
		IssuingEntity: "ETH",
		Attributes:    map[string]any{"deadline": "2026-12-01", "field_of_study": "Climate"},
	}
	r := Verify(opp, src, reg)
	if r.OK || r.Mismatch == "" {
		t.Fatalf("expected Mismatch, got %+v", r)
	}
}

func TestVerify_KindRequiredAttributeMissing(t *testing.T) {
	reg := newRegistry(t)
	src := &domain.Source{Kinds: []string{"scholarship"}}
	opp := &domain.ExternalOpportunity{
		Kind:          "scholarship",
		Title:         "MSc Climate Science",
		Description:   "Long description that is well above the minimum length required for verify to be happy.",
		IssuingEntity: "ETH",
		// Missing deadline (universal) + field_of_study (kind)
	}
	r := Verify(opp, src, reg)
	if r.OK {
		t.Fatal("expected fail")
	}
	if !contains(r.Missing, "deadline") || !contains(r.Missing, "field_of_study") {
		t.Fatalf("expected Missing to include deadline (universal) + field_of_study (kind), got %+v", r.Missing)
	}
}

func TestVerify_ScholarshipHappyPath(t *testing.T) {
	reg := newRegistry(t)
	src := &domain.Source{Kinds: []string{"scholarship"}}
	deadline := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	opp := &domain.ExternalOpportunity{
		Kind:          "scholarship",
		Title:         "MSc Climate Science",
		Description:   "Long description that is well above the minimum length required for verify to be happy.",
		IssuingEntity: "ETH Zurich",
		Deadline:      &deadline,
		Attributes:    map[string]any{"field_of_study": "Climate"},
	}
	r := Verify(opp, src, reg)
	if !r.OK {
		t.Fatalf("expected OK, got %+v", r)
	}
}

func TestVerify_HappyPath(t *testing.T) {
	reg := newRegistry(t)
	src := &domain.Source{Kinds: []string{"job"}}
	opp := &domain.ExternalOpportunity{
		Kind:           "job",
		Title:          "Senior Go Engineer",
		Description:    "Long description that is well above the minimum length required for verify to be happy.",
		IssuingEntity:  "Acme",
		ApplyURL:       "https://acme.example/apply",
		AnchorLocation: &domain.Location{Country: "KE"},
		Attributes:     map[string]any{"employment_type": "full-time"},
	}
	r := Verify(opp, src, reg)
	if !r.OK {
		t.Fatalf("expected OK, got %+v", r)
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
