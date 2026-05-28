package candidatestore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

// ErrProfileNotFound surfaces when no candidate_profile row exists.
var ErrProfileNotFound = errors.New("candidatestore: profile not found")

// ProfileFields is the ATS-autofill bag returned by
// GET /api/me/profile-fields. Field shape and naming mirror common ATS
// form fields (full_name, current_title, etc.) so the browser
// extension can mostly write directly into the page.
type ProfileFields struct {
	CandidateID     string           `json:"candidate_id"`
	CurrentTitle    string           `json:"current_title,omitempty"`
	TargetJobTitle  string           `json:"target_job_title,omitempty"`
	Seniority       string           `json:"seniority,omitempty"`
	ExperienceLevel string           `json:"experience_level,omitempty"`
	YearsExperience int              `json:"years_experience,omitempty"`
	Skills          []string         `json:"skills,omitempty"`
	StrongSkills    []string         `json:"strong_skills,omitempty"`
	WorkingSkills   []string         `json:"working_skills,omitempty"`
	ToolsFrameworks []string         `json:"tools_frameworks,omitempty"`
	Certifications  []string         `json:"certifications,omitempty"`
	PreferredRoles  []string         `json:"preferred_roles,omitempty"`
	Industries      []string         `json:"industries,omitempty"`
	Education       string           `json:"education,omitempty"`
	Languages       []string         `json:"languages,omitempty"`
	Bio             string           `json:"bio,omitempty"`
	Locations       []string         `json:"preferred_locations,omitempty"`
	Countries       []string         `json:"preferred_countries,omitempty"`
	RemotePref      string           `json:"remote_preference,omitempty"`
	SalaryMin       float32          `json:"salary_min,omitempty"`
	SalaryMax       float32          `json:"salary_max,omitempty"`
	Currency        string           `json:"currency,omitempty"`
	WorkHistory     []map[string]any `json:"work_history,omitempty"`
}

// GetProfileFields fetches one candidate's profile and returns the
// ATS-autofill payload + a stable ETag.
func GetProfileFields(ctx context.Context, db *sql.DB, candidateID string) (*ProfileFields, string, error) {
	const q = `
SELECT COALESCE(current_title,''),
       COALESCE(target_job_title,''),
       COALESCE(seniority,''),
       COALESCE(experience_level,''),
       COALESCE(years_experience,0),
       COALESCE(skills,           ARRAY[]::text[]),
       COALESCE(strong_skills,    ARRAY[]::text[]),
       COALESCE(working_skills,   ARRAY[]::text[]),
       COALESCE(tools_frameworks, ARRAY[]::text[]),
       COALESCE(certifications,''),
       COALESCE(preferred_roles,''),
       COALESCE(industries,''),
       COALESCE(education,''),
       COALESCE(languages,''),
       COALESCE(bio,''),
       COALESCE(preferred_locations,''),
       COALESCE(preferred_countries,''),
       COALESCE(remote_preference,''),
       COALESCE(salary_min,0),
       COALESCE(salary_max,0),
       COALESCE(currency,''),
       COALESCE(work_history,'[]')::text,
       updated_at,
       cv_scored_at
  FROM candidate_profiles WHERE id = $1
`
	var (
		pf            ProfileFields
		certsRaw      string
		preferredRaw  string
		industriesRaw string
		languagesRaw  string
		locationsRaw  string
		countriesRaw  string
		workHistRaw   string
		updatedAt     time.Time
		cvScoredAt    sql.NullTime
	)
	err := db.QueryRowContext(ctx, q, candidateID).Scan(
		&pf.CurrentTitle, &pf.TargetJobTitle, &pf.Seniority, &pf.ExperienceLevel,
		&pf.YearsExperience,
		pq.Array(&pf.Skills),
		pq.Array(&pf.StrongSkills),
		pq.Array(&pf.WorkingSkills),
		pq.Array(&pf.ToolsFrameworks),
		&certsRaw,
		&preferredRaw, &industriesRaw, &pf.Education,
		&languagesRaw, &pf.Bio,
		&locationsRaw, &countriesRaw, &pf.RemotePref,
		&pf.SalaryMin, &pf.SalaryMax, &pf.Currency,
		&workHistRaw, &updatedAt, &cvScoredAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrProfileNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("candidatestore: profile-fields: %w", err)
	}
	pf.CandidateID = candidateID
	pf.Certifications = splitCSV(certsRaw)
	pf.PreferredRoles = splitCSV(preferredRaw)
	pf.Industries = splitCSV(industriesRaw)
	pf.Languages = splitCSV(languagesRaw)
	pf.Locations = splitCSV(locationsRaw)
	pf.Countries = splitCSV(countriesRaw)
	if workHistRaw != "" && workHistRaw != "[]" {
		_ = json.Unmarshal([]byte(workHistRaw), &pf.WorkHistory)
	}

	etag := computeETag(updatedAt, cvScoredAt.Time)
	return &pf, etag, nil
}

// splitCSV parses a comma-separated value into a deduped, trimmed slice.
// Empty input → nil.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func computeETag(updatedAt, cvScoredAt time.Time) string {
	h := sha256.New()
	_, _ = h.Write([]byte(updatedAt.UTC().Format(time.RFC3339Nano)))
	h.Write([]byte("|"))
	_, _ = h.Write([]byte(cvScoredAt.UTC().Format(time.RFC3339Nano)))
	sum := hex.EncodeToString(h.Sum(nil))
	return `W/"` + sum[:16] + `"`
}
