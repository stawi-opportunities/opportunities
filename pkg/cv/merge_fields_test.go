package cv

import (
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
