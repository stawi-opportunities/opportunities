package cv

import (
	"strings"
	"testing"
	"time"
)

func TestBuildMatchAwareReport(t *testing.T) {
	overall := &CVStrengthReport{
		OverallScore: 72,
		Components:   ScoreComponents{ATS: 70, Keywords: 65, Impact: 80, RoleFit: 75, Clarity: 70},
		TargetRole:   "Backend Engineer",
		GeneratedAt:  time.Now().UTC(),
	}
	cvText := "Backend Engineer with Go, PostgreSQL, Kubernetes experience shipping APIs."
	jobs := []MatchedJob{
		{Title: "Go Backend Engineer", Company: "Acme", Score: 0.82, Snippet: "Go PostgreSQL Kubernetes APIs"},
		{Title: "Java Developer", Company: "Other", Score: 0.4, Snippet: "Java Spring Hibernate"},
	}
	r := BuildMatchAwareReport(overall, cvText, jobs)
	if r.JobsScored != 2 {
		t.Fatalf("jobs_scored=%d", r.JobsScored)
	}
	if r.MatchedJobs[0].FitScore <= r.MatchedJobs[1].FitScore {
		t.Fatalf("expected Go job higher fit: %d vs %d", r.MatchedJobs[0].FitScore, r.MatchedJobs[1].FitScore)
	}
	html := RenderMatchAwareHTML(r)
	if !strings.Contains(html, "CV ATS Report") || !strings.Contains(html, "Backend Engineer") {
		t.Fatalf("html missing expected content")
	}
}
