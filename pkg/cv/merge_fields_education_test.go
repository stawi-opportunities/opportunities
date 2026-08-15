package cv

import (
	"strings"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/candidatestore"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
)

func TestMergeEducation_FromAIHistoryOnly(t *testing.T) {
	extracted := &extraction.CVFields{
		EducationHistory: []extraction.EducationEntry{
			{School: "MIT", Degree: "BSc", Field: "CS", StartDate: "2014", EndDate: "2018", Notes: "Honors"},
			{School: "Stanford", Degree: "MSc", Field: "AI", StartDate: "2019", EndDate: "2021"},
		},
	}
	merged, filled := MergeExtractedIntoProfile(nil, extracted, ParsedContact{})
	if len(merged.EducationHistory) != 2 {
		t.Fatalf("history=%+v filled=%v", merged.EducationHistory, filled)
	}
	school, _ := merged.EducationHistory[0]["school"].(string)
	if school != "MIT" {
		t.Fatalf("school=%q", school)
	}
	if !strings.Contains(merged.Education, "MIT") {
		t.Fatalf("summary=%q", merged.Education)
	}
}

func TestMergeEducation_DoesNotInventStructureFromFreeText(t *testing.T) {
	// Free-text only — code must NOT regex-split into fake school/degree rows.
	extracted := &extraction.CVFields{
		Education: `MSc Data Science — Stanford University (2020–2022)
BSc Computer Science — University of Nairobi (2014–2018)`,
	}
	merged, _ := MergeExtractedIntoProfile(nil, extracted, ParsedContact{})
	if len(merged.EducationHistory) != 0 {
		t.Fatalf("must not invent education_history from free text: %+v", merged.EducationHistory)
	}
	if !strings.Contains(merged.Education, "Stanford") {
		t.Fatalf("should keep free-text education: %q", merged.Education)
	}
}

func TestMergeEducation_DoesNotOverwriteExistingHistory(t *testing.T) {
	existing := &candidatestore.ProfileFields{
		EducationHistory: []map[string]any{
			{"school": "Keep Me U", "degree": "BA"},
		},
		Education: "BA — Keep Me U",
	}
	extracted := &extraction.CVFields{
		EducationHistory: []extraction.EducationEntry{
			{School: "Other", Degree: "MSc"},
		},
	}
	merged, _ := MergeExtractedIntoProfile(existing, extracted, ParsedContact{})
	if len(merged.EducationHistory) != 1 {
		t.Fatalf("overwrote history: %+v", merged.EducationHistory)
	}
	if merged.EducationHistory[0]["school"] != "Keep Me U" {
		t.Fatalf("school=%v", merged.EducationHistory[0]["school"])
	}
}
