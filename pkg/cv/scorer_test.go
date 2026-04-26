package cv

import (
	"context"
	"strings"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/extraction"
)

func TestScoreATS_fullStructure(t *testing.T) {
	cv := strings.Repeat(
		"Professional Summary\nBackend engineer with 8 years of experience building distributed systems, scaling infrastructure, and shipping user-facing features across multiple product surfaces and teams. Mentor to junior engineers and owner of platform reliability metrics end to end.\n\nExperience\nAcme Corp - Senior Software Engineer (2021-present)\nLed backend platform team of 5 engineers. Shipped payment processing service handling 1M transactions per day.\n\nPrior Co - Software Engineer (2018-2021)\nBuilt APIs and services for customer onboarding and billing flows.\n\nSkills\nGo, Python, Kubernetes, Docker, PostgreSQL, Redis, React, TypeScript, AWS, CI/CD, REST, Microservices\n\nEducation\nBSc Computer Science - University College (2014-2018)\n",
		1,
	)
	fields := &extraction.CVFields{
		WorkHistory: []extraction.WorkHistoryEntry{
			{Company: "Acme", Title: "Engineer", Summary: "Built things"},
		},
	}
	score := scoreATS(cv, fields)
	if score < 80 {
		t.Errorf("well-formed CV should score >= 80 on ATS, got %d", score)
	}
}

func TestScoreATS_missingEverything(t *testing.T) {
	// No section headers, no summary hints, no work history.
	cv := "Random paragraph that never mentions any structured CV markers at all."
	score := scoreATS(cv, nil)
	if score > 45 {
		t.Errorf("structureless CV should score <= 45, got %d", score)
	}
}

func TestScoreKeywords_programmingHit(t *testing.T) {
	cv := "Built REST APIs in Go and Node.js with Kubernetes, Docker, Postgres, Redis, CI/CD, React, TypeScript, Python, Java, testing, AWS, cloud, git, microservices, graphql, api, rust, javascript"
	score := scoreKeywords(cv, FamilyProgramming)
	if score < 80 {
		t.Errorf("keyword-rich CV should score >= 80, got %d", score)
	}
}

func TestScoreKeywords_empty(t *testing.T) {
	score := scoreKeywords("lorem ipsum", FamilyProgramming)
	if score > 10 {
		t.Errorf("empty CV should score <= 10, got %d", score)
	}
}

func TestKeywordSynonymMatches(t *testing.T) {
	cv := "Built services in golang and k8s, used postgres"
	if !keywordPresent(strings.ToLower(cv), "go") {
		t.Error("golang should match go via synonym")
	}
	if !keywordPresent(strings.ToLower(cv), "kubernetes") {
		t.Error("k8s should match kubernetes")
	}
	if !keywordPresent(strings.ToLower(cv), "postgresql") {
		t.Error("postgres should match postgresql")
	}
}

func TestScoreBullet(t *testing.T) {
	cases := []struct {
		name    string
		bullet  string
		wantMin int
		wantMax int
	}{
		{"weak verb", "Worked on backend services", 0, 45},
		{"strong verb + metric", "Built Go services handling 2M requests/min with 99.99% uptime", 75, 100},
		{"strong verb no metric", "Built backend APIs for the payments platform", 55, 80},
		{"quantified + outcome", "Reduced API latency by 40% resulting in 2x throughput", 85, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scoreBullet(tc.bullet)
			if got < tc.wantMin || got > tc.wantMax {
				t.Errorf("scoreBullet(%q) = %d, want [%d, %d]", tc.bullet, got, tc.wantMin, tc.wantMax)
			}
		})
	}
}

func TestDetectRoleFamily(t *testing.T) {
	cases := map[string]RoleFamily{
		"Full Stack Developer":      FamilyProgramming,
		"Senior Software Engineer":  FamilyProgramming,
		"Site Reliability Engineer": FamilyDevOps,
		"DevOps Engineer":           FamilyDevOps,
		"Data Scientist":            FamilyData,
		"Machine Learning Engineer": FamilyData,
		"Product Designer":          FamilyDesign,
		"Growth Marketing Manager":  FamilyMarketing, // "marketing" matches before "manager"
		"VP of Engineering":         FamilyManagement,
		"Chief Operating Officer":   FamilyManagement,
		"Accountant":                FamilyGeneral,
	}
	for title, want := range cases {
		if got := DetectRoleFamily(title); got != want {
			t.Errorf("DetectRoleFamily(%q) = %q, want %q", title, got, want)
		}
	}
}

func TestWeightedOverall(t *testing.T) {
	// All 100 → 100
	c := ScoreComponents{ATS: 100, Keywords: 100, Impact: 100, RoleFit: 100, Clarity: 100}
	if got := weightedOverall(c); got != 100 {
		t.Errorf("all-100 components → %d, want 100", got)
	}
	// All 0 → 0
	c = ScoreComponents{}
	if got := weightedOverall(c); got != 0 {
		t.Errorf("all-0 components → %d, want 0", got)
	}
	// Mixed — impact is weighted 25%, others 15-20%
	// 0,0,100,0,0 → 25
	c = ScoreComponents{Impact: 100}
	if got := weightedOverall(c); got != 25 {
		t.Errorf("impact-only components → %d, want 25", got)
	}
}

// Nil embedder must not panic and must fall through to the neutral
// role-fit score.
func TestScore_nilEmbedder(t *testing.T) {
	s := NewScorer(nil)
	report := s.Score(context.Background(), "Some CV text with Experience, Skills, Education sections.", nil, "Software Engineer")
	if report == nil {
		t.Fatal("report must not be nil with a nil embedder")
		return
	}
	if report.Components.RoleFit != 60 {
		t.Errorf("nil embedder → role fit = 60, got %d", report.Components.RoleFit)
	}
}

// scoreImpact on a mix of strong + weak bullets should land between the
// two extremes (not at either end).
func TestScoreImpact_mixBullets(t *testing.T) {
	fields := &extraction.CVFields{
		WorkHistory: []extraction.WorkHistoryEntry{
			{Summary: "Built Go services handling 2M requests/min resulting in 99% uptime\nWorked on APIs"},
		},
	}
	got := scoreImpact(fields)
	// "Built Go services ..." is strong (≥75), "Worked on APIs" is weak (≤45).
	if got <= 45 || got >= 90 {
		t.Errorf("mixed-bullet impact = %d, expected somewhere between the two extremes", got)
	}
}

// Each buzzword costs 5 points; three buzzwords → at most 85.
func TestScoreClarity_buzzwords(t *testing.T) {
	cv := "Strong synergy, a real rockstar ninja on our team."
	got := scoreClarity(cv)
	if got > 85 {
		t.Errorf("scoreClarity with 3 buzzwords = %d, want <= 85", got)
	}
}

// Passive-voice penalty: >3 hits → at least -10; >8 → additional -10.
func TestScoreClarity_passiveVoiceHits(t *testing.T) {
	// Four passive constructions (was/were + ed/en).
	cvFour := "The system was deployed. The code was reviewed. The bugs were fixed. The docs were updated."
	scoreFour := scoreClarity(cvFour)
	if scoreFour > 90 {
		t.Errorf(">3 passive hits should shave at least 10 points, got %d", scoreFour)
	}

	// Nine passive constructions.
	cvNine := strings.Repeat("The system was deployed. ", 9)
	scoreNine := scoreClarity(cvNine)
	if scoreNine > 80 {
		t.Errorf(">8 passive hits should shave at least 20 points, got %d", scoreNine)
	}
	if scoreNine >= scoreFour {
		t.Errorf("nine-hit score (%d) should be strictly lower than four-hit (%d)", scoreNine, scoreFour)
	}
}

// Multi-line summary with "- " prefixes should split on newlines and
// drop the bullet marker.
func TestSplitBullets_newlineFirst(t *testing.T) {
	in := "- Built APIs\n- Shipped services\n* Led migration"
	got := splitBullets(in)
	want := []string{"Built APIs", "Shipped services", "Led migration"}
	if len(got) != len(want) {
		t.Fatalf("got %d bullets, want %d (%v)", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("splitBullets[%d] = %q, want %q", i, got[i], w)
		}
	}
}

// Single-line summary should fall back to sentence split.
func TestSplitBullets_sentenceFallback(t *testing.T) {
	in := "Built APIs. Shipped services. Led migration"
	got := splitBullets(in)
	if len(got) < 3 {
		t.Errorf("sentence fallback produced %d bullets, want 3 (%v)", len(got), got)
	}
}

// Same text → same hash, different text → different hash.
func TestVersionHashText_stable(t *testing.T) {
	a := versionHash("hello world")
	b := versionHash("hello world")
	if a != b {
		t.Errorf("same-text hashes differ: %q vs %q", a, b)
	}
	c := versionHash("hello world ")
	if a == c {
		t.Errorf("different-text hashes collided: %q == %q", a, c)
	}
}

// Exported wrapper matches the internal.
func TestVersionHashText_expose(t *testing.T) {
	want := versionHash("abc")
	if got := VersionHashText("abc"); got != want {
		t.Errorf("VersionHashText mismatch: got %q, want %q", got, want)
	}
}
