package v1

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"strings"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/cv"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

// ToolsEmbedder is the narrow embed surface job-fit needs (extraction.Extractor).
type ToolsEmbedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// ToolsDeps wires free career tools (CV ATS score, job fitness).
type ToolsDeps struct {
	DB       *sql.DB
	Scorer   *cv.Scorer
	Embedder ToolsEmbedder
	// Index supplies the candidate's stored match embedding when present.
	Index *matching.IndexStore
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
// Uses vector similarity when embeddings are available, blended with keyword
// overlap for explainable signals.
//
// Body: { "job_text": "...", "opportunity_id": "optional", "title": "optional" }
func JobFitHandler(deps ToolsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			httpmw.ProblemJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
			return
		}
		ctx := r.Context()
		log := util.Log(ctx)
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
		oppID := strings.TrimSpace(in.OpportunityID)
		if jobText == "" && oppID != "" && deps.DB != nil {
			var title, desc sql.NullString
			err := deps.DB.QueryRowContext(ctx, `
SELECT COALESCE(title,''), COALESCE(description,'') FROM opportunities
WHERE canonical_id = $1 OR slug = $1 LIMIT 1`, oppID).Scan(&title, &desc)
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
		kwScore := fit.Score

		// Vector path: candidate index embedding × job embedding (stored or live).
		if vec, method, ok := vectorJobFit(ctx, deps, candidateID, oppID, jobText); ok {
			// Blend: 70% vector similarity, 30% keyword score for explainability.
			blended := int(math.Round(0.70*float64(vec) + 0.30*float64(kwScore)))
			if blended > 100 {
				blended = 100
			}
			fit.Score = blended
			fit.Label = fitLabel(blended)
			fit.Method = method
			fit.VectorScore = &vec
			fit.KeywordScore = &kwScore
			fit.Signals = append([]string{"Semantic similarity (AI embeddings) used"}, fit.Signals...)
			if blended >= 65 && (len(fit.Suggestions) == 0 || fit.Suggestions[0] != "Strong fit — apply with a short note highlighting the shared keywords") {
				// Prefer a semantic-aware suggestion when vector path lifted score.
				if kwScore < 65 {
					fit.Suggestions = append([]string{
						"Strong semantic fit — tailor the first bullet of your summary to this role and apply",
					}, fit.Suggestions...)
				}
			}
		} else {
			fit.Method = "keywords"
			fit.KeywordScore = &kwScore
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fit)
		log.WithField("candidate_id", candidateID).WithField("score", fit.Score).
			WithField("method", fit.Method).Debug("tools/job-fit: scored")
	}
}

// JobFitResult is the free job-fitness tool response.
type JobFitResult struct {
	Score        int      `json:"score"`
	Label        string   `json:"label"`
	Signals      []string `json:"signals"`
	Suggestions  []string `json:"suggestions"`
	Title        string   `json:"title,omitempty"`
	Method       string   `json:"method,omitempty"` // "keywords" | "vector+stored" | "vector+live"
	VectorScore  *int     `json:"vector_score,omitempty"`
	KeywordScore *int     `json:"keyword_score,omitempty"`
}

func fitLabel(score int) string {
	switch {
	case score >= 65:
		return "strong"
	case score >= 40:
		return "moderate"
	default:
		return "weak"
	}
}

// vectorJobFit returns a 0–100 semantic score when both vectors are available.
func vectorJobFit(
	ctx context.Context,
	deps ToolsDeps,
	candidateID, opportunityID, jobText string,
) (score int, method string, ok bool) {
	candVec := candidateEmbedding(ctx, deps, candidateID)
	if len(candVec) == 0 {
		return 0, "", false
	}

	// Prefer stored opportunity embedding when opportunity_id is known.
	if opportunityID != "" && deps.DB != nil {
		if oppVec, err := loadOpportunityEmbedding(ctx, deps.DB, opportunityID); err == nil && len(oppVec) == len(candVec) {
			sim := cosineSim(candVec, oppVec)
			return int(math.Round(sim * 100)), "vector+stored", true
		}
	}

	// Live-embed job text (passage prefix matches index convention).
	if deps.Embedder == nil {
		return 0, "", false
	}
	passage := jobText
	if !strings.HasPrefix(passage, extraction.EmbedPassagePrefix) {
		passage = extraction.EmbedPassagePrefix + truncateRunes(jobText, 1800)
	}
	jobVec, err := deps.Embedder.Embed(ctx, passage)
	if err != nil || len(jobVec) == 0 || len(jobVec) != len(candVec) {
		util.Log(ctx).WithError(err).Debug("tools/job-fit: live embed failed")
		return 0, "", false
	}
	sim := cosineSim(candVec, jobVec)
	return int(math.Round(sim * 100)), "vector+live", true
}

func candidateEmbedding(ctx context.Context, deps ToolsDeps, candidateID string) []float32 {
	if deps.Index != nil {
		if idx, err := deps.Index.Get(ctx, candidateID); err == nil && idx != nil && len(idx.Embedding) > 0 {
			return idx.Embedding
		}
	}
	// Fallback: embed profile text live.
	if deps.Embedder == nil || deps.DB == nil {
		return nil
	}
	profile := loadProfileText(ctx, deps.DB, candidateID)
	if len(profile) < 40 {
		return nil
	}
	vec, err := deps.Embedder.Embed(ctx, extraction.EmbedPassagePrefix+truncateRunes(profile, 1800))
	if err != nil {
		return nil
	}
	return vec
}

func loadOpportunityEmbedding(ctx context.Context, db *sql.DB, idOrSlug string) ([]float32, error) {
	var embText string
	err := db.QueryRowContext(ctx, `
SELECT embedding::text FROM opportunities
WHERE (canonical_id = $1 OR slug = $1) AND embedding IS NOT NULL
LIMIT 1`, idOrSlug).Scan(&embText)
	if err != nil {
		return nil, err
	}
	vec := matching.ParseVectorLiteral(embText)
	if len(vec) == 0 {
		return nil, sql.ErrNoRows
	}
	return vec, nil
}

func cosineSim(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		af, bf := float64(a[i]), float64(b[i])
		dot += af * bf
		na += af * af
		nb += bf * bf
	}
	if na == 0 || nb == 0 {
		return 0
	}
	sim := dot / (math.Sqrt(na) * math.Sqrt(nb))
	// Clamp to [0,1] for display (cosine can be slightly negative).
	if sim < 0 {
		return 0
	}
	if sim > 1 {
		return 1
	}
	return sim
}

func heuristicJobFit(profile, job, title string) JobFitResult {
	p := strings.ToLower(profile)
	j := strings.ToLower(job)
	if p == "" {
		return JobFitResult{
			Score: 0, Label: "weak", Method: "keywords",
			Signals:     []string{"No profile text — upload a CV for a better score"},
			Suggestions: []string{"Upload your CV in Preferences", "Set a target job title"},
			Title:       title,
		}
	}
	pTokens := significantTokens(p)
	jTokens := significantTokens(j)
	if len(jTokens) == 0 {
		return JobFitResult{Score: 0, Label: "weak", Method: "keywords", Title: title}
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
		Score: score, Label: fitLabel(score), Method: "keywords",
		Signals: signals, Suggestions: suggestions, Title: title,
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
