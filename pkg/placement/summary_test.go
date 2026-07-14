package placement

import (
	"strings"
	"testing"
)

func TestBuildDocument_CombinesQualAndPrefs(t *testing.T) {
	min, max := 90000.0, 120000.0
	f := Fields{
		TargetJobTitle:     "Senior Software Engineer",
		ExperienceLevel:    "senior",
		JobTypes:           []string{"Full-time"},
		SalaryMin:          &min,
		SalaryMax:          &max,
		Currency:           "USD",
		PreferredCountries: []string{"KE", "NG"},
		ExtraInfo: `CURRICULUM VITAE
Jane Doe
EXPERIENCE
7 years building APIs in Go at Acme.
EDUCATION
BSc Computer Science
SKILLS
Go, Kubernetes, PostgreSQL`,
	}
	doc := BuildDocument("cand_1", f)
	if !doc.Ready {
		t.Fatalf("expected ready, missing=%v", doc.Missing)
	}
	if !strings.Contains(doc.SummaryText, "Senior Software Engineer") {
		t.Fatalf("summary missing role: %s", doc.SummaryText)
	}
	if !strings.Contains(doc.SummaryText, "Qualifications") {
		t.Fatal("missing qualifications section")
	}
	if !strings.Contains(doc.SummaryText, "Preferences") {
		t.Fatal("missing preferences section")
	}
	if !strings.Contains(doc.SummaryText, "KE") {
		t.Fatal("missing countries")
	}
	if !strings.Contains(doc.QualificationsText, "Kubernetes") {
		t.Fatal("qual text should include CV skills")
	}
}

func TestGuidedFollowUp_AsksForCV(t *testing.T) {
	min := 80000.0
	f := Fields{
		TargetJobTitle:     "Product Manager",
		ExperienceLevel:    "mid",
		JobTypes:           []string{"Full-time"},
		SalaryMin:          &min,
		PreferredCountries: []string{"NG"},
	}
	reply := GuidedFollowUp(f)
	if !strings.Contains(strings.ToLower(reply), "cv") && !strings.Contains(strings.ToLower(reply), "resume") {
		t.Fatalf("expected CV ask, got %q", reply)
	}
	if !strings.Contains(reply, "Product Manager") {
		t.Fatalf("expected acknowledgment, got %q", reply)
	}
}

func TestMissingRequired_Order(t *testing.T) {
	miss := MissingRequired(Fields{})
	if len(miss) == 0 || miss[0] != "target_job_title" {
		t.Fatalf("expected role first, got %v", miss)
	}
}

func TestFiltersFromFields(t *testing.T) {
	min := 50000.0
	f := FiltersFromFields(Fields{
		PreferredCountries: []string{"ke", "NG"},
		SalaryMin:          &min,
	})
	if len(f.Countries) != 2 || f.Countries[0] != "KE" {
		t.Fatalf("countries=%v", f.Countries)
	}
	if f.SalaryFloorUSD == nil || *f.SalaryFloorUSD != 50000 {
		t.Fatalf("floor=%v", f.SalaryFloorUSD)
	}
}
