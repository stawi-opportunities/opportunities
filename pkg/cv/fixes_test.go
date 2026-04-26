package cv

import (
	"strings"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/extraction"
)

// helper: does the fix list contain a given fix ID?
func hasFix(fixes []PriorityFix, id string) bool {
	for _, f := range fixes {
		if f.ID == id {
			return true
		}
	}
	return false
}

func findFix(fixes []PriorityFix, id string) *PriorityFix {
	for i := range fixes {
		if fixes[i].ID == id {
			return &fixes[i]
		}
	}
	return nil
}

// A CV string that contains no "summary"/"skills"/"about"/"profile"
// keywords so the ATS section fixes fire.
const bareCVText = "Just some narrative prose without any structural markers."

func TestDetectPriorityFixes_addSummaryAndSkills(t *testing.T) {
	fields := &extraction.CVFields{}
	c := ScoreComponents{ATS: 100, Keywords: 100, Impact: 100, RoleFit: 100, Clarity: 100}
	fixes := detectPriorityFixes(bareCVText, fields, FamilyProgramming, c)

	if !hasFix(fixes, "add-summary") {
		t.Errorf("expected add-summary fix; got IDs: %v", fixIDs(fixes))
	}
	if !hasFix(fixes, "add-skills-section") {
		t.Errorf("expected add-skills-section fix; got IDs: %v", fixIDs(fixes))
	}
}

func TestDetectPriorityFixes_addWorkHistoryWhenEmpty(t *testing.T) {
	// cv text includes summary + skills so those fixes don't fire.
	cvText := "Professional Summary: backend. Skills: Go."
	fields := &extraction.CVFields{WorkHistory: nil}
	c := ScoreComponents{ATS: 100, Keywords: 100, Impact: 100, RoleFit: 100, Clarity: 100}
	fixes := detectPriorityFixes(cvText, fields, FamilyProgramming, c)

	fx := findFix(fixes, "add-work-history")
	if fx == nil {
		t.Fatalf("expected add-work-history fix; got IDs: %v", fixIDs(fixes))
		return
	}
	if fx.Impact != "high" {
		t.Errorf("add-work-history impact = %q, want high", fx.Impact)
	}
}

func TestDetectPriorityFixes_addKeywordsWhenLow(t *testing.T) {
	cvText := "summary about something. skills: basic knowledge. nothing technical."
	fields := &extraction.CVFields{
		WorkHistory: []extraction.WorkHistoryEntry{{Company: "A", Title: "T", Summary: "Built APIs"}},
	}
	c := ScoreComponents{ATS: 100, Keywords: 40, Impact: 100, RoleFit: 100, Clarity: 100}
	fixes := detectPriorityFixes(cvText, fields, FamilyProgramming, c)

	fx := findFix(fixes, "add-keywords")
	if fx == nil {
		t.Fatalf("expected add-keywords fix; got IDs: %v", fixIDs(fixes))
		return
	}
	if len(fx.Suggestions) == 0 {
		t.Error("add-keywords fix should have non-empty Suggestions")
	}
}

func TestDetectPriorityFixes_strengthenBulletsAndWeakVerbs(t *testing.T) {
	cvText := "summary. skills: Go."
	fields := &extraction.CVFields{
		WorkHistory: []extraction.WorkHistoryEntry{
			{Company: "A", Title: "Eng", Summary: "Responsible for backend services\nWorked on API design"},
		},
	}
	c := ScoreComponents{ATS: 100, Keywords: 100, Impact: 40, RoleFit: 100, Clarity: 100}
	fixes := detectPriorityFixes(cvText, fields, FamilyProgramming, c)

	if !hasFix(fixes, "strengthen-bullets") {
		t.Errorf("expected strengthen-bullets; got: %v", fixIDs(fixes))
	}
	if !hasFix(fixes, "replace-weak-verbs") {
		t.Errorf("expected replace-weak-verbs; got: %v", fixIDs(fixes))
	}
}

func TestDetectPriorityFixes_removeBuzzwords(t *testing.T) {
	cvText := "summary. skills: Go. I am a rockstar ninja guru who brings synergy."
	fields := &extraction.CVFields{
		WorkHistory: []extraction.WorkHistoryEntry{{Company: "A", Title: "T", Summary: "Built APIs"}},
	}
	c := ScoreComponents{ATS: 100, Keywords: 100, Impact: 100, RoleFit: 100, Clarity: 40}
	fixes := detectPriorityFixes(cvText, fields, FamilyProgramming, c)

	if !hasFix(fixes, "remove-buzzwords") {
		t.Errorf("expected remove-buzzwords; got: %v", fixIDs(fixes))
	}
}

func TestDetectPriorityFixes_sortedByImpact(t *testing.T) {
	// Compose a CV that triggers multiple fixes across impact buckets.
	cvText := "" // no summary, no skills → two high-impact ATS fixes
	fields := &extraction.CVFields{
		WorkHistory: []extraction.WorkHistoryEntry{
			{Company: "A", Title: "T", Summary: "Responsible for backend\nWorked on APIs"},
		},
	}
	// Keywords 40 (<50 → high); Impact 40 → high; RoleFit 40 → medium; Clarity 40 → low.
	c := ScoreComponents{ATS: 100, Keywords: 40, Impact: 40, RoleFit: 40, Clarity: 40}
	fixes := detectPriorityFixes(cvText, fields, FamilyProgramming, c)

	// Confirm ordering: impact ranks must be non-decreasing.
	prev := -1
	for _, f := range fixes {
		r := impactRank(f.Impact)
		if r < prev {
			t.Errorf("fixes not sorted by impact; IDs=%v", fixIDs(fixes))
			break
		}
		prev = r
	}
}

func TestDetectPriorityFixes_respectsCap(t *testing.T) {
	// Trigger every category at once.
	cvText := "rockstar ninja guru synergy" // no summary, no skills, buzzwords
	fields := &extraction.CVFields{
		WorkHistory: []extraction.WorkHistoryEntry{
			{Company: "A", Title: "T", Summary: "Responsible for backend\nWorked on APIs\nHelped debug"},
		},
	}
	c := ScoreComponents{ATS: 10, Keywords: 10, Impact: 10, RoleFit: 10, Clarity: 10}
	fixes := detectPriorityFixes(cvText, fields, FamilyProgramming, c)

	if len(fixes) > maxPriorityFixes {
		t.Errorf("expected <= %d fixes, got %d (%v)", maxPriorityFixes, len(fixes), fixIDs(fixes))
	}
}

func fixIDs(fs []PriorityFix) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.ID
	}
	return out
}

func TestMissingKeywords_limitsAndOrder(t *testing.T) {
	// CV mentions only "go" and "docker" — everything else is missing.
	cv := "I use go and docker daily"
	got := missingKeywords(cv, FamilyProgramming, 3)

	if len(got) > 3 {
		t.Errorf("missingKeywords limit violated: %v", got)
	}
	// The returned keywords should be in expectedKeywords order. Build a
	// list of expected keywords not present in the CV and verify prefix.
	lower := strings.ToLower(cv)
	var wanted []string
	for _, kw := range expectedKeywords[FamilyProgramming] {
		if !keywordPresent(lower, kw) {
			wanted = append(wanted, kw)
			if len(wanted) == len(got) {
				break
			}
		}
	}
	for i := range got {
		if got[i] != wanted[i] {
			t.Errorf("missingKeywords[%d] = %q, want %q (full got=%v, wanted=%v)", i, got[i], wanted[i], got, wanted)
		}
	}
}

func TestMissingKeywords_noneMissing(t *testing.T) {
	cv := strings.Join(expectedKeywords[FamilyProgramming], " ")
	if got := missingKeywords(cv, FamilyProgramming, 6); len(got) != 0 {
		t.Errorf("expected none missing, got %v", got)
	}
}

func TestCountWeakBullets(t *testing.T) {
	if got := countWeakBullets(nil); got != 0 {
		t.Errorf("nil fields → %d, want 0", got)
	}
	fields := &extraction.CVFields{
		WorkHistory: []extraction.WorkHistoryEntry{
			{Summary: "Worked on backend\nBuilt APIs handling 2M requests/min with 99% uptime"},
			{Summary: "Helped the team debug\nResponsible for CI"},
		},
	}
	// Bullets: weak(worked on), strong(built ... 2M), weak(helped), weak(responsible)
	got := countWeakBullets(fields)
	if got != 3 {
		t.Errorf("countWeakBullets = %d, want 3", got)
	}
}

func TestCountBulletsWithWeakVerb(t *testing.T) {
	fields := &extraction.CVFields{
		WorkHistory: []extraction.WorkHistoryEntry{
			{Summary: "Worked on backend\nBuilt APIs"},
			{Summary: "helped the team\nResponsible for CI\nLed migration"},
		},
	}
	got := countBulletsWithWeakVerb(fields)
	if got != 3 {
		t.Errorf("countBulletsWithWeakVerb = %d, want 3", got)
	}
	if countBulletsWithWeakVerb(nil) != 0 {
		t.Error("nil fields must yield 0")
	}
}

func TestWeakestBullets_orderAndLimit(t *testing.T) {
	fields := &extraction.CVFields{
		WorkHistory: []extraction.WorkHistoryEntry{
			// work_index 0 — one strong, one weak
			{Summary: "Built Go services handling 2M requests/min resulting in 99.99% uptime\nWorked on APIs"},
			// work_index 1 — two weaks (tie-breaker: later work index first)
			{Summary: "Responsible for backend\nHelped team debug"},
		},
	}
	got := weakestBullets(fields, 3)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	// Scores non-decreasing.
	for i := 1; i < len(got); i++ {
		if got[i].Score < got[i-1].Score {
			t.Errorf("not sorted by score: %+v", got)
		}
	}
	// Among the two weak bullets (scores tied), WorkIndex 1 comes first.
	if got[0].WorkIndex != 1 {
		t.Errorf("tie-break should prefer later work index first; got WorkIndex=%d", got[0].WorkIndex)
	}
}

func TestBuildRewriteSystemPrompt_containsRoleAndJSONHints(t *testing.T) {
	prompt := buildRewriteSystemPrompt("Senior Backend Engineer", "programming")
	for _, needle := range []string{
		"Senior Backend Engineer",
		"programming",
		"JSON",
		"curly braces",
	} {
		if !strings.Contains(prompt, needle) {
			t.Errorf("prompt missing %q: %s", needle, prompt)
		}
	}
}
