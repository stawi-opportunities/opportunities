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

// ProfileFields is the ATS/CV hub bag for GET/PUT /me/profile-fields.
// Contact details are never stored here — only cv_contact_ids (IDs of
// standalone ProfileService contacts). Transient contact_details on PUT
// are accepted by the handler and turned into CreateContact calls only.
type ProfileFields struct {
	CandidateID string `json:"candidate_id"`
	Name        string `json:"name,omitempty"`
	// ContactDetails is write-only on PUT: opaque strings sent to
	// ProfileService CreateContact. Never persisted on candidate_profiles.
	ContactDetails []string `json:"contact_details,omitempty"`
	// CVContactIDs is read-only on GET: stored platform contact ids.
	CVContactIDs     []string         `json:"cv_contact_ids,omitempty"`
	CurrentTitle     string           `json:"current_title,omitempty"`
	TargetJobTitle   string           `json:"target_job_title,omitempty"`
	Seniority        string           `json:"seniority,omitempty"`
	ExperienceLevel  string           `json:"experience_level,omitempty"`
	YearsExperience  int              `json:"years_experience,omitempty"`
	Skills           []string         `json:"skills,omitempty"`
	StrongSkills     []string         `json:"strong_skills,omitempty"`
	WorkingSkills    []string         `json:"working_skills,omitempty"`
	ToolsFrameworks  []string         `json:"tools_frameworks,omitempty"`
	Certifications   []string         `json:"certifications,omitempty"`
	PreferredRoles   []string         `json:"preferred_roles,omitempty"`
	Industries       []string         `json:"industries,omitempty"`
	Education        string           `json:"education,omitempty"`
	Languages        []string         `json:"languages,omitempty"`
	Bio              string           `json:"bio,omitempty"`
	Locations        []string         `json:"preferred_locations,omitempty"`
	Countries        []string         `json:"preferred_countries,omitempty"`
	Regions          []string         `json:"preferred_regions,omitempty"`
	Timezones        []string         `json:"preferred_timezones,omitempty"`
	RemotePref       string           `json:"remote_preference,omitempty"`
	JobSearchStatus  string           `json:"job_search_status,omitempty"`
	SalaryMin        float32          `json:"salary_min,omitempty"`
	SalaryMax        float32          `json:"salary_max,omitempty"`
	Currency         string           `json:"currency,omitempty"`
	USWorkAuth       *bool            `json:"us_work_auth,omitempty"`
	NeedsSponsorship *bool            `json:"needs_sponsorship,omitempty"`
	WorkHistory      []map[string]any `json:"work_history,omitempty"`
}

// GetProfileFields fetches one candidate's profile (no phone/email columns).
func GetProfileFields(ctx context.Context, db *sql.DB, candidateID string) (*ProfileFields, string, error) {
	const q = `
SELECT COALESCE(name,''),
       COALESCE(current_title,''),
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
       COALESCE(preferred_regions,''),
       COALESCE(preferred_timezones,''),
       COALESCE(remote_preference,''),
       COALESCE(job_search_status,''),
       COALESCE(salary_min,0),
       COALESCE(salary_max,0),
       COALESCE(currency,''),
       us_work_auth,
       needs_sponsorship,
       COALESCE(work_history,'[]')::text,
       COALESCE(cv_contact_ids, ARRAY[]::text[]),
       updated_at,
       cv_scored_at
  FROM candidate_profiles WHERE id = $1 OR profile_id = $1
`
	var (
		pf            ProfileFields
		certsRaw      string
		preferredRaw  string
		industriesRaw string
		languagesRaw  string
		locationsRaw  string
		countriesRaw  string
		regionsRaw    string
		timezonesRaw  string
		workHistRaw   string
		contactIDs    pq.StringArray
		usAuth        sql.NullBool
		needsSpon     sql.NullBool
		updatedAt     time.Time
		cvScoredAt    sql.NullTime
	)
	err := db.QueryRowContext(ctx, q, candidateID).Scan(
		&pf.Name,
		&pf.CurrentTitle, &pf.TargetJobTitle, &pf.Seniority, &pf.ExperienceLevel,
		&pf.YearsExperience,
		pq.Array(&pf.Skills),
		pq.Array(&pf.StrongSkills),
		pq.Array(&pf.WorkingSkills),
		pq.Array(&pf.ToolsFrameworks),
		&certsRaw,
		&preferredRaw, &industriesRaw, &pf.Education,
		&languagesRaw, &pf.Bio,
		&locationsRaw, &countriesRaw, &regionsRaw, &timezonesRaw,
		&pf.RemotePref, &pf.JobSearchStatus,
		&pf.SalaryMin, &pf.SalaryMax, &pf.Currency,
		&usAuth, &needsSpon,
		&workHistRaw,
		&contactIDs,
		&updatedAt, &cvScoredAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrProfileNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("candidatestore: profile-fields: %w", err)
	}
	pf.CandidateID = candidateID
	pf.CVContactIDs = cleanIDs(contactIDs)
	pf.Certifications = splitCSV(certsRaw)
	pf.PreferredRoles = splitCSV(preferredRaw)
	pf.Industries = splitCSV(industriesRaw)
	pf.Languages = splitCSV(languagesRaw)
	pf.Locations = splitCSV(locationsRaw)
	pf.Countries = splitCSV(countriesRaw)
	pf.Regions = splitCSV(regionsRaw)
	pf.Timezones = splitCSV(timezonesRaw)
	if usAuth.Valid {
		v := usAuth.Bool
		pf.USWorkAuth = &v
	}
	if needsSpon.Valid {
		v := needsSpon.Bool
		pf.NeedsSponsorship = &v
	}
	if workHistRaw != "" && workHistRaw != "[]" {
		_ = json.Unmarshal([]byte(workHistRaw), &pf.WorkHistory)
	}

	etag := computeETag(updatedAt, cvScoredAt.Time)
	return &pf, etag, nil
}

// PutProfileFields updates CV hub fields. Never writes phone/email plaintext.
func PutProfileFields(ctx context.Context, db *sql.DB, candidateID string, pf *ProfileFields) error {
	if db == nil {
		return fmt.Errorf("candidatestore: db is nil")
	}
	if candidateID == "" {
		return fmt.Errorf("candidatestore: candidate_id required")
	}
	if pf == nil {
		return fmt.Errorf("candidatestore: profile fields required")
	}

	workJSON, err := json.Marshal(pf.WorkHistory)
	if err != nil {
		return fmt.Errorf("candidatestore: marshal work_history: %w", err)
	}
	if pf.WorkHistory == nil {
		workJSON = []byte("[]")
	}

	skills := pf.Skills
	if len(skills) == 0 {
		skills = pf.StrongSkills
	}

	res, err := db.ExecContext(ctx, `
UPDATE candidate_profiles SET
  name = COALESCE(NULLIF($2, ''), name),
  current_title = $3,
  target_job_title = $4,
  seniority = $5,
  experience_level = $6,
  years_experience = $7,
  skills = $8,
  strong_skills = $9,
  working_skills = $10,
  tools_frameworks = $11,
  certifications = $12,
  preferred_roles = $13,
  industries = $14,
  education = $15,
  languages = $16,
  bio = $17,
  preferred_locations = $18,
  preferred_countries = $19,
  preferred_regions = $20,
  preferred_timezones = $21,
  remote_preference = $22,
  job_search_status = $23,
  salary_min = $24,
  salary_max = $25,
  currency = $26,
  us_work_auth = $27,
  needs_sponsorship = $28,
  work_history = $29::jsonb,
  updated_at = NOW()
WHERE id = $1 OR profile_id = $1`,
		candidateID,
		pf.Name,
		pf.CurrentTitle,
		pf.TargetJobTitle,
		pf.Seniority,
		pf.ExperienceLevel,
		pf.YearsExperience,
		pq.Array(skills),
		pq.Array(pf.StrongSkills),
		pq.Array(pf.WorkingSkills),
		pq.Array(pf.ToolsFrameworks),
		joinCSV(pf.Certifications),
		joinCSV(pf.PreferredRoles),
		joinCSV(pf.Industries),
		pf.Education,
		joinCSV(pf.Languages),
		pf.Bio,
		joinCSV(pf.Locations),
		joinCSV(pf.Countries),
		joinCSV(pf.Regions),
		joinCSV(pf.Timezones),
		pf.RemotePref,
		pf.JobSearchStatus,
		pf.SalaryMin,
		pf.SalaryMax,
		pf.Currency,
		pf.USWorkAuth,
		pf.NeedsSponsorship,
		string(workJSON),
	)
	if err != nil {
		return fmt.Errorf("candidatestore: put profile-fields: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrProfileNotFound
	}
	return nil
}

func joinCSV(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ", ")
}

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
