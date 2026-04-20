package domain_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"stawi.jobs/pkg/domain"
)

func fixedJob() *domain.CanonicalJob {
	posted := time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC)
	expires := posted.AddDate(0, 4, 0)
	return &domain.CanonicalJob{
		BaseModel:        domain.BaseModel{ID: "cr7qs3q8j1hci9fn3sag"},
		Slug:             "senior-go-developer-at-acme-corp-a3f7b2",
		Title:            "Senior Go Developer",
		Company:          "Acme Corp",
		Description:      "We are hiring.",
		Category:         "programming",
		LocationText:     "Lagos, Nigeria (Remote)",
		Country:          "NG",
		RemoteType:       "remote",
		EmploymentType:   "full_time",
		Seniority:        "senior",
		SalaryMin:        80000,
		SalaryMax:        120000,
		Currency:         "USD",
		RequiredSkills:   "Go, PostgreSQL",
		NiceToHaveSkills: "Kubernetes",
		ApplyURL:         "https://acme.com/careers/senior-go",
		PostedAt:         &posted,
		ExpiresAt:        &expires,
		QualityScore:     85,
		Status:           "active",
	}
}

func TestBuildSnapshot_AllFields(t *testing.T) {
	snap := domain.BuildSnapshot(fixedJob())

	if snap.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", snap.SchemaVersion)
	}
	if snap.Slug != "senior-go-developer-at-acme-corp-a3f7b2" {
		t.Errorf("slug = %q", snap.Slug)
	}
	if snap.Company.Name != "Acme Corp" || snap.Company.Slug != "acme-corp" {
		t.Errorf("company = %+v", snap.Company)
	}
	if snap.Location.RemoteType != "remote" || snap.Location.Country != "NG" {
		t.Errorf("location = %+v", snap.Location)
	}
	if snap.Employment.Type != "full_time" || snap.Employment.Seniority != "senior" {
		t.Errorf("employment = %+v", snap.Employment)
	}
	if snap.Compensation.Min != 80000 || snap.Compensation.Max != 120000 {
		t.Errorf("compensation = %+v", snap.Compensation)
	}
	if len(snap.Skills.Required) != 2 || snap.Skills.Required[0] != "Go" {
		t.Errorf("skills.required = %v", snap.Skills.Required)
	}
	if !snap.IsFeatured {
		t.Errorf("quality=85 should be featured")
	}
	if snap.PostedAt == "" || snap.ExpiresAt == "" {
		t.Errorf("timestamps not populated")
	}
}

func TestBuildSnapshot_JSONContractStable(t *testing.T) {
	b, err := json.Marshal(domain.BuildSnapshot(fixedJob()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	for _, want := range []string{
		`"schema_version":1`,
		`"slug":"senior-go-developer-at-acme-corp-a3f7b2"`,
		`"title":"Senior Go Developer"`,
		`"category":"programming"`,
		`"remote_type":"remote"`,
		`"is_featured":true`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("marshaled JSON missing %q\n  got %s", want, s)
		}
	}
}

func TestBuildSnapshot_EmptySkillsSerializeAsArray(t *testing.T) {
	j := fixedJob()
	j.RequiredSkills = ""
	j.NiceToHaveSkills = ""
	b, _ := json.Marshal(domain.BuildSnapshot(j))
	if !strings.Contains(string(b), `"required":[]`) {
		t.Errorf("empty required should serialize as [], got %s", string(b))
	}
}

func TestBuildSnapshot_WithHTMLIsWired(t *testing.T) {
	snap := domain.BuildSnapshotWithHTML(fixedJob(), "<p>safe</p>")
	if snap.DescriptionHTML != "<p>safe</p>" {
		t.Errorf("description_html = %q", snap.DescriptionHTML)
	}
}

func TestBuildSnapshot_QualityBelowThresholdNotFeatured(t *testing.T) {
	j := fixedJob()
	j.QualityScore = 79
	if domain.BuildSnapshot(j).IsFeatured {
		t.Error("quality 79 should not be featured")
	}
}
