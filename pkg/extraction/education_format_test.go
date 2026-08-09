package extraction

import (
	"strings"
	"testing"
)

func TestFormatEducationSummary(t *testing.T) {
	s := FormatEducationSummary([]EducationEntry{
		{School: "MIT", Degree: "BSc", Field: "Computer Science", StartDate: "2014", EndDate: "2018", Notes: "First Class"},
		{School: "Stanford", Degree: "MSc", Field: "AI", StartDate: "2019", EndDate: "2021"},
	})
	if s == "" {
		t.Fatal("empty summary")
	}
	for _, part := range []string{"MIT", "Stanford", "BSc", "Computer Science"} {
		if !strings.Contains(s, part) {
			t.Fatalf("missing %q in %q", part, s)
		}
	}
	if strings.Count(s, "\n") != 1 {
		t.Fatalf("expected 2 lines, got %q", s)
	}
}
