package cv

import (
	"strings"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/candidatestore"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
)

func TestMergeExtractedDoesNotOverwrite(t *testing.T) {
	existing := &candidatestore.ProfileFields{
		Name:         "Keep Me",
		Phone:        "",
		CurrentTitle: "Engineer",
	}
	extracted := &extraction.CVFields{
		Name:         "From CV",
		Phone:        "+1 555 0100",
		CurrentTitle: "Senior Engineer",
		StrongSkills: []string{"Go", "SQL"},
	}
	merged, filled := MergeExtractedIntoProfile(existing, extracted, ParsedContact{})
	if merged.Name != "Keep Me" {
		t.Fatalf("name overwritten: %q", merged.Name)
	}
	if merged.Phone != "+1 555 0100" {
		t.Fatalf("phone not filled: %q", merged.Phone)
	}
	if merged.CurrentTitle != "Engineer" {
		t.Fatalf("title overwritten: %q", merged.CurrentTitle)
	}
	if len(merged.StrongSkills) != 2 {
		t.Fatalf("skills: %#v", merged.StrongSkills)
	}
	// name must not appear in filled
	for _, k := range filled {
		if k == "name" || k == "current_title" {
			t.Fatalf("should not report filled %q", k)
		}
	}
}

func TestMergeFromHeuristicOnly(t *testing.T) {
	merged, filled := MergeExtractedIntoProfile(nil, nil, ParsedContact{
		Name: "Ada Lovelace", Email: "ada@example.com", Phone: "+44 20 7946 0958",
	})
	if merged.Name != "Ada Lovelace" || merged.Phone == "" {
		t.Fatalf("merged=%+v filled=%v", merged, filled)
	}
}

func TestMergeMultiContactAndBioFromSummarySection(t *testing.T) {
	raw := `Jane A. Doe
jane@work.com
personal@mail.com
+254 712 345 678
+254 733 000 111

PROFESSIONAL SUMMARY
Seasoned backend engineer with 10 years building payments platforms
across East Africa. Led teams of 8.

SKILLS
Go, PostgreSQL, Kubernetes, gRPC

CERTIFICATIONS
AWS Solutions Architect Associate
CKA

EXPERIENCE
Acme — Staff Engineer
`
	contact := ParseContactFromText(raw)
	extracted := &extraction.CVFields{
		Name:         "Jane A. Doe",
		Emails:       []string{"jane@work.com", "personal@mail.com"},
		Phones:       []string{"+254 712 345 678", "+254 733 000 111"},
		Bio:          "Short stub.",
		StrongSkills: []string{"Go"},
		// missing certs on purpose — heuristic should fill
	}
	merged, filled := MergeExtractedIntoProfileWithText(nil, extracted, contact, raw)
	if merged.Name != "Jane A. Doe" {
		t.Fatalf("name=%q", merged.Name)
	}
	if !strings.Contains(merged.Phone, "712") || !strings.Contains(merged.Phone, "733") {
		t.Fatalf("expected multi phone, got %q", merged.Phone)
	}
	if len(merged.Emails) < 2 {
		t.Fatalf("emails=%v", merged.Emails)
	}
	if !strings.Contains(merged.Bio, "payments platforms") {
		t.Fatalf("bio should use summary section, got %q filled=%v", merged.Bio, filled)
	}
	if len(merged.StrongSkills) < 1 {
		t.Fatalf("skills empty")
	}
	// working skills should pick up remaining from skills section
	if len(merged.WorkingSkills)+len(merged.StrongSkills) < 3 {
		t.Fatalf("expected heuristic skills union strong=%v working=%v", merged.StrongSkills, merged.WorkingSkills)
	}
	if len(merged.Certifications) == 0 {
		t.Fatalf("certs not filled from section: %v filled=%v", merged.Certifications, filled)
	}
}

func TestMergeKeepsFullWorkHistorySummary(t *testing.T) {
	long := strings.Repeat("Built payment rails. ", 20)
	extracted := &extraction.CVFields{
		WorkHistory: []extraction.WorkHistoryEntry{
			{Company: "Acme", Title: "Engineer", StartDate: "2020", EndDate: "present", Summary: long},
		},
	}
	merged, _ := MergeExtractedIntoProfile(nil, extracted, ParsedContact{})
	if len(merged.WorkHistory) != 1 {
		t.Fatal("expected work history")
	}
	desc, _ := merged.WorkHistory[0]["description"].(string)
	if desc != long {
		t.Fatalf("summary truncated: len=%d want=%d", len(desc), len(long))
	}
}
