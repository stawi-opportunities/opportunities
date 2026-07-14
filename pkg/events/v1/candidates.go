package eventsv1

import (
	"encoding/json"
	"time"
)

// CVUploadedV1 is emitted by the candidates HTTP upload handler after
// the raw bytes have been stored (files service or R2 archive) and the
// plain-text has been extracted + indexed locally. It is the entry point
// of the candidate lifecycle.
//
// CVVersion is monotonic per candidate (increments on every successful
// upload with new content). The first upload is version 1.
//
// RawArchiveRef / ContentURI point at durable binary storage.
// FileID is set when the platform files service accepted the upload.
// ExtractedText is also written to candidate_cv_documents for local use.
type CVUploadedV1 struct {
	CandidateID   string `json:"candidate_id"   `
	CVVersion     int    `json:"cv_version"     `
	RawArchiveRef string `json:"raw_archive_ref"`
	Filename      string `json:"filename,omitempty"    `
	ContentType   string `json:"content_type,omitempty"`
	SizeBytes     int64  `json:"size_bytes,omitempty"  `

	// FileID is the platform files-service media id when Storage is "files".
	FileID string `json:"file_id,omitempty"`
	// ContentURI is the durable content URI (files) or archive key.
	ContentURI string `json:"content_uri,omitempty"`
	// ContentHash is sha256 of the raw bytes.
	ContentHash string `json:"content_hash,omitempty"`
	// Storage is "files" or "archive".
	Storage string `json:"storage,omitempty"`

	// ExtractedText is the plain-text conversion of the uploaded PDF/DOCX.
	ExtractedText string `json:"extracted_text"`

	EventID    string    `json:"-"`
	OccurredAt time.Time `json:"-"`
}

// CVExtractedV1 carries the parsed CV fields plus the deterministic
// scoring output. Emitted by the cv-extract handler after ExtractCV
// + Scorer.Score run.
//
// Field shapes mostly mirror extraction.CVFields, except SalaryMin /
// SalaryMax are integers here (normalised from the LLM's free-form
// string output — see field comment below).
type CVExtractedV1 struct {
	CandidateID string `json:"candidate_id"`
	CVVersion   int    `json:"cv_version"  `

	// CVFields (flattened; matches extraction.CVFields shape).
	Name               string   `json:"name,omitempty"               `
	Email              string   `json:"email,omitempty"              `
	Phone              string   `json:"phone,omitempty"              `
	Location           string   `json:"location,omitempty"           `
	CurrentTitle       string   `json:"current_title,omitempty"      `
	Bio                string   `json:"bio,omitempty"                `
	Seniority          string   `json:"seniority,omitempty"          `
	YearsExperience    int      `json:"years_experience,omitempty"   `
	PrimaryIndustry    string   `json:"primary_industry,omitempty"   `
	StrongSkills       []string `json:"strong_skills,omitempty"      `
	WorkingSkills      []string `json:"working_skills,omitempty"     `
	ToolsFrameworks    []string `json:"tools_frameworks,omitempty"   `
	Certifications     []string `json:"certifications,omitempty"     `
	PreferredRoles     []string `json:"preferred_roles,omitempty"    `
	Languages          []string `json:"languages,omitempty"          `
	Education          string   `json:"education,omitempty"          `
	PreferredLocations []string `json:"preferred_locations,omitempty"`
	RemotePreference   string   `json:"remote_preference,omitempty"  `
	// SalaryMin / SalaryMax are the normalised integer form. The LLM
	// extraction (extraction.CVFields) emits these as strings like
	// "80000" or "85k"; the cv-extract handler (Task 7) is responsible
	// for parsing them into integers, clamping non-parseable outputs
	// to 0, and converting units. Downstream consumers read integers.
	SalaryMin int    `json:"salary_min,omitempty"`
	SalaryMax int    `json:"salary_max,omitempty"`
	Currency  string `json:"currency,omitempty"           `

	// Score components (matches cv.ScoreComponents).
	ScoreATS      int `json:"score_ats"     `
	ScoreKeywords int `json:"score_keywords"`
	ScoreImpact   int `json:"score_impact"  `
	ScoreRoleFit  int `json:"score_role_fit"`
	ScoreClarity  int `json:"score_clarity" `
	ScoreOverall  int `json:"score_overall" `

	ModelVersionExtract string `json:"model_version_extract,omitempty"`
	ModelVersionScore   string `json:"model_version_score,omitempty"  `

	EventID    string    `json:"-"`
	OccurredAt time.Time `json:"-"`
}

// CVFix mirrors cv.PriorityFix, carried inside CVImprovedV1.
type CVFix struct {
	FixID string `json:"fix_id"            `
	Title string `json:"title,omitempty"   `
	// ImpactLevel is serialised as "impact" on the wire to keep the
	// JSON terse and to mirror cv.PriorityFix.Impact (the struct
	// field this is produced from).
	ImpactLevel    string `json:"impact,omitempty"`
	Category       string `json:"category,omitempty"`
	Why            string `json:"why,omitempty"     `
	AutoApplicable bool   `json:"auto_applicable"   `
	Rewrite        string `json:"rewrite,omitempty" `
}

// CVImprovedV1 is emitted by the cv-improve handler after deterministic
// fix detection + LLM rewrite attachment.
type CVImprovedV1 struct {
	CandidateID  string  `json:"candidate_id"`
	CVVersion    int     `json:"cv_version"  `
	Fixes        []CVFix `json:"fixes"       `
	ModelVersion string  `json:"model_version,omitempty"`

	EventID    string    `json:"-"`
	OccurredAt time.Time `json:"-"`
}

// CandidateEmbeddingV1 is emitted by the cv-embed handler after a
// successful Embed() call on the CV text.
type CandidateEmbeddingV1 struct {
	CandidateID  string    `json:"candidate_id"`
	CVVersion    int       `json:"cv_version"  `
	Vector       []float32 `json:"vector"      `
	ModelVersion string    `json:"model_version,omitempty"`

	EventID    string    `json:"-"`
	OccurredAt time.Time `json:"-"`
}

// PreferencesUpdatedV1 carries the candidate's per-kind preferences as
// opaque JSON blobs. Each opt-in is the kind-specific preferences struct
// (JobPreferences, ScholarshipPreferences, etc.) marshalled to JSON.
// Consumers unmarshal the relevant kind blob into its concrete prefs type.
//
// Each event is a replace-all snapshot of the candidate's opt-ins, not
// a delta. The map key is the kind ID ("job", "scholarship", "tender",
// "deal", "funding"); a missing key means the candidate has not opted
// into that kind.
type PreferencesUpdatedV1 struct {
	CandidateID string                     `json:"candidate_id"`
	OptIns      map[string]json.RawMessage `json:"opt_ins"     `
	UpdatedAt   time.Time                  `json:"updated_at"  `

	EventID    string    `json:"-"`
	OccurredAt time.Time `json:"-"`
}

// MatchRow is one candidate-to-job match, carried inside MatchesReadyV1.
type MatchRow struct {
	CanonicalID string  `json:"canonical_id"          `
	ApplyURL    string  `json:"apply_url"             `
	Score       float64 `json:"score"                 `
	RerankScore float64 `json:"rerank_score,omitempty"`
}

// MatchesReadyV1 is emitted by the match endpoint (or the Trustage
// weekly-digest admin) after a candidate's top matches are ranked.
type MatchesReadyV1 struct {
	CandidateID  string     `json:"candidate_id"  `
	MatchBatchID string     `json:"match_batch_id"`
	Matches      []MatchRow `json:"matches"       `

	EventID    string    `json:"-"`
	OccurredAt time.Time `json:"-"`
}

// DigestJob is one new-job item carried inside WeeklyJobsDigestV1.
// Shape is intentionally minimal — the notification service builds
// the canonical URL from `Slug` and looks up nothing else.
type DigestJob struct {
	CanonicalID string    `json:"canonical_id"`
	Title       string    `json:"title"`
	ApplyURL    string    `json:"apply_url"`
	Company     string    `json:"company,omitempty"`
	Country     string    `json:"country,omitempty"`
	Kind        string    `json:"kind"`
	Slug        string    `json:"slug"`
	PostedAt    time.Time `json:"posted_at"`
}

// DigestStatBucket is one row of a top-N analytics breakdown.
type DigestStatBucket struct {
	Code  string `json:"code"`
	Count int    `json:"count"`
}

// DigestStats carries the headline analytics block shown above the
// jobs list. All counts are over the same 7-day window as the jobs.
type DigestStats struct {
	TotalNewThisWeek int                `json:"total_new_this_week"`
	TopCountries     []DigestStatBucket `json:"top_countries,omitempty"`
	TopKinds         []DigestStatBucket `json:"top_kinds,omitempty"`
}

// WeeklyJobsDigestV1 is emitted by the matching app's weekly admin
// handler once per unpaid candidate. The notification service is the
// sole consumer.
//
// Personalisation lives in `Country` and the `Kind` field of each
// embedded job — the rendering side decides whether to localise
// based on `Locale`. `PlansURL` is included so the template doesn't
// have to assume the host (allows reuse on preview deploys).
type WeeklyJobsDigestV1 struct {
	CandidateID string      `json:"candidate_id"`
	Country     string      `json:"country,omitempty"`
	Locale      string      `json:"locale,omitempty"`
	Jobs        []DigestJob `json:"jobs"`
	Stats       DigestStats `json:"stats"`
	PlansURL    string      `json:"plans_url"`

	EventID    string    `json:"-"`
	OccurredAt time.Time `json:"-"`
}
