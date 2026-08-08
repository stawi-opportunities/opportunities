package cv

import (
	"fmt"
	"html"
	"strings"
	"time"
)

// MatchedJob is a compact job card used when scoring a CV against
// preference-matched opportunities.
type MatchedJob struct {
	OpportunityID string
	Title         string
	Company       string
	Score         float64 // match engine score 0–1
	ApplyURL      string
	// Snippet is title + description excerpt used for keyword/role-fit.
	Snippet string
}

// MatchAwareReport is the comprehensive paid ATS report: overall CV
// strength plus fit vs each matched preference job.
type MatchAwareReport struct {
	Overall       *CVStrengthReport `json:"overall"`
	MatchedJobs   []JobFitLine      `json:"matched_jobs"`
	AvgMatchFit   int               `json:"avg_match_fit"`
	JobsScored    int               `json:"jobs_scored"`
	GeneratedAt   time.Time         `json:"generated_at"`
	CandidateHint string            `json:"candidate_hint,omitempty"`
}

// JobFitLine is one row in the match-aware report.
type JobFitLine struct {
	Title      string   `json:"title"`
	Company    string   `json:"company"`
	MatchScore float64  `json:"match_score"` // engine score
	FitScore   int      `json:"fit_score"`   // 0–100 CV vs job text
	Signals    []string `json:"signals,omitempty"`
	ApplyURL   string   `json:"apply_url,omitempty"`
}

// BuildMatchAwareReport combines a CVStrengthReport with per-job keyword
// fitness against the candidate's delivered matches.
func BuildMatchAwareReport(
	overall *CVStrengthReport,
	cvText string,
	jobs []MatchedJob,
) *MatchAwareReport {
	out := &MatchAwareReport{
		Overall:     overall,
		GeneratedAt: time.Now().UTC(),
		MatchedJobs: make([]JobFitLine, 0, len(jobs)),
	}
	if overall == nil {
		overall = &CVStrengthReport{}
		out.Overall = overall
	}

	sumFit := 0
	for _, j := range jobs {
		snippet := strings.TrimSpace(j.Snippet)
		if snippet == "" {
			snippet = strings.TrimSpace(j.Title + " " + j.Company)
		}
		fit, signals := keywordJobFit(cvText, snippet, j.Title)
		out.MatchedJobs = append(out.MatchedJobs, JobFitLine{
			Title:      j.Title,
			Company:    j.Company,
			MatchScore: j.Score,
			FitScore:   fit,
			Signals:    signals,
			ApplyURL:   j.ApplyURL,
		})
		sumFit += fit
		out.JobsScored++
	}
	if out.JobsScored > 0 {
		out.AvgMatchFit = sumFit / out.JobsScored
	}
	return out
}

func keywordJobFit(cvText, jobText, title string) (int, []string) {
	cv := strings.ToLower(cvText)
	job := strings.ToLower(jobText)
	if len(cv) < 20 || len(job) < 10 {
		return 0, []string{"insufficient text"}
	}
	// Tokenize job on non-letters; score overlap of meaningful tokens.
	tokens := tokenizeWords(job)
	if len(tokens) == 0 {
		return 0, nil
	}
	hits := 0
	var signals []string
	for _, t := range tokens {
		if len(t) < 4 {
			continue
		}
		if strings.Contains(cv, t) {
			hits++
			if len(signals) < 8 {
				signals = append(signals, t)
			}
		}
	}
	denom := 0
	for _, t := range tokens {
		if len(t) >= 4 {
			denom++
		}
	}
	if denom == 0 {
		denom = 1
	}
	score := (hits * 100) / denom
	if score > 100 {
		score = 100
	}
	// Slight boost when title words appear in CV.
	for _, tw := range tokenizeWords(strings.ToLower(title)) {
		if len(tw) >= 4 && strings.Contains(cv, tw) {
			score += 5
			break
		}
	}
	if score > 100 {
		score = 100
	}
	return score, signals
}

func tokenizeWords(s string) []string {
	var b strings.Builder
	var out []string
	flush := func() {
		if b.Len() == 0 {
			return
		}
		out = append(out, b.String())
		b.Reset()
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

// RenderMatchAwareHTML builds a standalone HTML document suitable for
// email body and as a downloadable attachment.
func RenderMatchAwareHTML(r *MatchAwareReport) string {
	if r == nil {
		return "<html><body><p>No report.</p></body></html>"
	}
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"/>
<title>Stawi CV ATS Report</title>
<style>
body{font-family:system-ui,sans-serif;max-width:720px;margin:0 auto;padding:1.5rem;color:#0f172a;line-height:1.45}
h1{font-size:1.5rem;margin:0 0 .25rem}
h2{font-size:1.1rem;margin:1.5rem 0 .5rem;border-bottom:1px solid #e2e8f0;padding-bottom:.25rem}
.meta{color:#64748b;font-size:.85rem;margin-bottom:1rem}
.score{font-size:2.5rem;font-weight:700}
.grid{display:grid;grid-template-columns:repeat(5,1fr);gap:.5rem;margin:1rem 0}
.card{border:1px solid #e2e8f0;border-radius:8px;padding:.75rem;text-align:center}
.card span{display:block;font-size:.7rem;text-transform:uppercase;color:#64748b}
.job{border:1px solid #e2e8f0;border-radius:8px;padding:.75rem;margin:.5rem 0}
.job h3{margin:0 0 .25rem;font-size:1rem}
.fix{margin:.5rem 0;padding:.75rem;background:#f8fafc;border-radius:8px}
</style></head><body>`)
	b.WriteString("<h1>CV ATS Report</h1>")
	fmt.Fprintf(&b, `<p class="meta">Generated %s · Stawi Opportunities</p>`,
		html.EscapeString(r.GeneratedAt.Format(time.RFC1123)))

	if r.Overall != nil {
		fmt.Fprintf(&b, `<p class="score">%d<span style="font-size:1rem;font-weight:500"> / 100 overall</span></p>`, r.Overall.OverallScore)
		if r.Overall.TargetRole != "" {
			fmt.Fprintf(&b, `<p class="meta">Target role: %s</p>`, html.EscapeString(r.Overall.TargetRole))
		}
		c := r.Overall.Components
		b.WriteString(`<div class="grid">`)
		for _, row := range []struct {
			l string
			n int
		}{
			{"ATS", c.ATS}, {"Keywords", c.Keywords}, {"Impact", c.Impact},
			{"Role fit", c.RoleFit}, {"Clarity", c.Clarity},
		} {
			fmt.Fprintf(&b, `<div class="card"><span>%s</span><strong>%d</strong></div>`, html.EscapeString(row.l), row.n)
		}
		b.WriteString(`</div>`)

		if len(r.Overall.PriorityFixes) > 0 {
			b.WriteString("<h2>Priority improvements</h2>")
			for _, f := range r.Overall.PriorityFixes {
				b.WriteString(`<div class="fix">`)
				fmt.Fprintf(&b, `<strong>%s</strong> <span class="meta">(%s · %s)</span>`,
					html.EscapeString(f.Title), html.EscapeString(f.Impact), html.EscapeString(f.Category))
				fmt.Fprintf(&b, `<p>%s</p>`, html.EscapeString(f.Why))
				b.WriteString(`</div>`)
			}
		}
		if len(r.Overall.Rewrites) > 0 {
			b.WriteString("<h2>Suggested rewrites</h2>")
			for _, rw := range r.Overall.Rewrites {
				b.WriteString(`<div class="fix">`)
				fmt.Fprintf(&b, `<p><strong>Before:</strong> %s</p>`, html.EscapeString(rw.Before))
				fmt.Fprintf(&b, `<p><strong>After:</strong> %s</p>`, html.EscapeString(rw.After))
				if rw.Reason != "" {
					fmt.Fprintf(&b, `<p class="meta">%s</p>`, html.EscapeString(rw.Reason))
				}
				b.WriteString(`</div>`)
			}
		}
	}

	b.WriteString("<h2>Fit vs your matched jobs</h2>")
	if r.JobsScored == 0 {
		b.WriteString(`<p class="meta">No matched jobs yet — overall CV score only. Complete preferences and wait for matches for a fuller report.</p>`)
	} else {
		fmt.Fprintf(&b, `<p class="meta">Average CV↔job fit: <strong>%d</strong>/100 across %d matches</p>`, r.AvgMatchFit, r.JobsScored)
		for _, j := range r.MatchedJobs {
			b.WriteString(`<div class="job">`)
			title := j.Title
			if title == "" {
				title = "Matched opportunity"
			}
			fmt.Fprintf(&b, `<h3>%s</h3>`, html.EscapeString(title))
			if j.Company != "" {
				fmt.Fprintf(&b, `<p class="meta">%s</p>`, html.EscapeString(j.Company))
			}
			fmt.Fprintf(&b, `<p>Match engine score: %.0f%% · CV fit: <strong>%d</strong>/100</p>`, j.MatchScore*100, j.FitScore)
			if len(j.Signals) > 0 {
				fmt.Fprintf(&b, `<p class="meta">Overlap: %s</p>`, html.EscapeString(strings.Join(j.Signals, ", ")))
			}
			b.WriteString(`</div>`)
		}
	}

	b.WriteString(`<p class="meta" style="margin-top:2rem">This report was generated after your $2 ATS report purchase. Re-run anytime after updating your CV or preferences.</p>`)
	b.WriteString(`</body></html>`)
	return b.String()
}
