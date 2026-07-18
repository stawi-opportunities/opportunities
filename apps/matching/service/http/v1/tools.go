package v1

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/cv"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

// ToolsDeps wires free career tools (CV ATS score, job fitness).
type ToolsDeps struct {
	DB     *sql.DB
	Scorer *cv.Scorer
}

// CVScoreHandler serves POST /me/tools/cv-score — free for signed-in users.
// Body: { "target_role": "optional", "cv_text": "optional paste" }.
func CVScoreHandler(deps ToolsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			httpmw.ProblemJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
			return
		}
		ctx := r.Context()
		log := util.Log(ctx)
		candidateID := httpmw.CandidateFromContext(ctx)
		if deps.Scorer == nil {
			httpmw.ProblemJSON(w, http.StatusServiceUnavailable, "scorer_unavailable", "CV scorer is not configured")
			return
		}

		var in struct {
			TargetRole string `json:"target_role"`
			CVText     string `json:"cv_text"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&in)
		}

		cvText := strings.TrimSpace(in.CVText)
		fields := &extraction.CVFields{}
		if cvText == "" && deps.DB != nil {
			cvText, fields = loadCVForScore(ctx, deps.DB, candidateID)
		}
		if len(cvText) < 40 {
			httpmw.ProblemJSON(w, http.StatusConflict, "cv_text_required",
				"upload a CV or paste at least a short resume text to score")
			return
		}

		report := deps.Scorer.Score(ctx, cvText, fields, strings.TrimSpace(in.TargetRole))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(report)
		log.WithField("candidate_id", candidateID).WithField("score", report.OverallScore).
			Debug("tools/cv-score: ok")
	}
}

// JobFitHandler serves POST /me/tools/job-fit — free fitness score for a role.
// Body: { "job_text": "...", "opportunity_id": "optional", "title": "optional" }
func JobFitHandler(deps ToolsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			httpmw.ProblemJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
			return
		}
		ctx := r.Context()
		candidateID := httpmw.CandidateFromContext(ctx)

		var in struct {
			JobText       string `json:"job_text"`
			OpportunityID string `json:"opportunity_id"`
			Title         string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil && r.ContentLength != 0 {
			httpmw.ProblemJSON(w, http.StatusBadRequest, "invalid_json", "invalid request body")
			return
		}
		jobText := strings.TrimSpace(in.JobText)
		if jobText == "" && in.OpportunityID != "" && deps.DB != nil {
			var title, desc sql.NullString
			err := deps.DB.QueryRowContext(ctx, `
SELECT COALESCE(title,''), COALESCE(description,'') FROM opportunities
WHERE canonical_id = $1 OR slug = $1 LIMIT 1`, strings.TrimSpace(in.OpportunityID)).Scan(&title, &desc)
			if err == nil {
				jobText = strings.TrimSpace(title.String + "\n\n" + stripHTMLLite(desc.String))
				if in.Title == "" {
					in.Title = title.String
				}
			}
		}
		if len(jobText) < 40 {
			httpmw.ProblemJSON(w, http.StatusBadRequest, "job_text_required",
				"paste a job description (or pass opportunity_id) of at least ~40 characters")
			return
		}

		profileText := loadProfileText(ctx, deps.DB, candidateID)
		fit := heuristicJobFit(profileText, jobText, in.Title)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fit)
	}
}

// JobFitResult is the free job-fitness tool response.
type JobFitResult struct {
	Score       int      `json:"score"`
	Label       string   `json:"label"`
	Signals     []string `json:"signals"`
	Suggestions []string `json:"suggestions"`
	Title       string   `json:"title,omitempty"`
}

func heuristicJobFit(profile, job, title string) JobFitResult {
	p := strings.ToLower(profile)
	j := strings.ToLower(job)
	if p == "" {
		return JobFitResult{
			Score: 0, Label: "weak",
			Signals:     []string{"No profile text — upload a CV for a better score"},
			Suggestions: []string{"Upload your CV in Preferences", "Set a target job title"},
			Title:       title,
		}
	}
	pTokens := significantTokens(p)
	jTokens := significantTokens(j)
	if len(jTokens) == 0 {
		return JobFitResult{Score: 0, Label: "weak", Title: title}
	}
	hits := 0
	var matched []string
	for t := range jTokens {
		if pTokens[t] {
			hits++
			if len(matched) < 8 {
				matched = append(matched, t)
			}
		}
	}
	ratio := float64(hits) / float64(len(jTokens))
	score := int(ratio * 100)
	if score > 100 {
		score = 100
	}
	titleHits := 0
	for _, w := range strings.Fields(strings.ToLower(title)) {
		if len(w) >= 4 && pTokens[w] {
			titleHits++
		}
	}
	if titleHits > 0 {
		score = minInt(100, score+10*titleHits)
	}

	label := "weak"
	switch {
	case score >= 65:
		label = "strong"
	case score >= 40:
		label = "moderate"
	}

	var signals []string
	if len(matched) > 0 {
		signals = append(signals, "Shared keywords: "+strings.Join(matched, ", "))
	}
	if titleHits > 0 {
		signals = append(signals, "Target title words appear in your profile")
	}
	if score < 40 {
		signals = append(signals, "Low keyword overlap — tailor your CV to this posting")
	}

	var suggestions []string
	if score < 65 {
		suggestions = append(suggestions,
			"Mirror exact skill phrases from the job into your Skills section",
			"Quantify impact in recent roles (metrics, scale, outcomes)",
		)
	} else {
		suggestions = append(suggestions,
			"Strong fit — apply with a short note highlighting the shared keywords",
		)
	}

	return JobFitResult{
		Score: score, Label: label, Signals: signals, Suggestions: suggestions, Title: title,
	}
}

func significantTokens(s string) map[string]bool {
	out := make(map[string]bool)
	for _, w := range strings.Fields(s) {
		w = strings.Trim(w, ".,;:()[]{}\"'`")
		if len(w) < 4 {
			continue
		}
		switch w {
		case "with", "from", "that", "this", "have", "will", "your", "their",
			"about", "into", "over", "under", "than", "then", "them", "were",
			"been", "being", "also", "such", "more", "most", "other", "some",
			"role", "team", "work", "working", "experience", "years", "using":
			continue
		}
		out[w] = true
	}
	return out
}

func loadCVForScore(ctx context.Context, db *sql.DB, candidateID string) (string, *extraction.CVFields) {
	fields := &extraction.CVFields{}
	var title, bio, skills sql.NullString
	_ = db.QueryRowContext(ctx, `
SELECT COALESCE(current_title,''), COALESCE(bio,''),
       COALESCE(array_to_string(strong_skills, ', '),'')
FROM candidate_profiles WHERE id = $1`, candidateID).Scan(&title, &bio, &skills)
	fields.CurrentTitle = title.String
	fields.Bio = bio.String
	if skills.String != "" {
		fields.StrongSkills = splitCSV(skills.String)
	}
	var summary sql.NullString
	_ = db.QueryRowContext(ctx, `
SELECT COALESCE(summary_text,'') FROM candidate_placement_profiles WHERE candidate_id = $1
ORDER BY updated_at DESC NULLS LAST LIMIT 1`, candidateID).Scan(&summary)
	if summary.String != "" {
		return summary.String, fields
	}
	return strings.TrimSpace(strings.Join([]string{title.String, bio.String, skills.String}, "\n")), fields
}

func loadProfileText(ctx context.Context, db *sql.DB, candidateID string) string {
	if db == nil {
		return ""
	}
	text, _ := loadCVForScore(ctx, db, candidateID)
	return text
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func stripHTMLLite(s string) string {
	// Descriptions may be HTML; strip tags for fitness tokens.
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
			b.WriteByte(' ')
		case !inTag:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
