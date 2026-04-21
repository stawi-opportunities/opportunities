package eventsv1

import "time"

// CVUploadedV1 is emitted by the candidates HTTP upload handler after
// the raw bytes have been archived to R2 and the plain-text has been
// extracted. It is the entry point of the candidate lifecycle.
//
// CVVersion is monotonic per candidate (increments on every successful
// upload). The first upload is version 1.
//
// RawArchiveRef is the R2 object key (e.g. "raw/abc123") returned by
// pkg/archive.PutRaw. Downstream cv-extract re-reads the raw bytes
// from this key; the extracted plain text is also carried inline for
// handler convenience in the normal path.
type CVUploadedV1 struct {
	CandidateID   string `json:"candidate_id"    parquet:"candidate_id"`
	CVVersion     int    `json:"cv_version"      parquet:"cv_version"`
	RawArchiveRef string `json:"raw_archive_ref" parquet:"raw_archive_ref"`
	Filename      string `json:"filename,omitempty"     parquet:"filename,optional"`
	ContentType   string `json:"content_type,omitempty" parquet:"content_type,optional"`
	SizeBytes     int64  `json:"size_bytes,omitempty"   parquet:"size_bytes,optional"`

	// ExtractedText is the plain-text conversion of the uploaded PDF/DOCX.
	ExtractedText string `json:"extracted_text" parquet:"extracted_text"`
}

// CVExtractedV1 carries the parsed CV fields plus the deterministic
// scoring output. Emitted by the cv-extract handler after ExtractCV
// + Scorer.Score run.
type CVExtractedV1 struct {
	CandidateID string `json:"candidate_id" parquet:"candidate_id"`
	CVVersion   int    `json:"cv_version"   parquet:"cv_version"`

	// CVFields (flattened; matches extraction.CVFields shape).
	Name               string   `json:"name,omitempty"                parquet:"name,optional"`
	Email              string   `json:"email,omitempty"               parquet:"email,optional"`
	Phone              string   `json:"phone,omitempty"               parquet:"phone,optional"`
	Location           string   `json:"location,omitempty"            parquet:"location,optional"`
	CurrentTitle       string   `json:"current_title,omitempty"       parquet:"current_title,optional"`
	Bio                string   `json:"bio,omitempty"                 parquet:"bio,optional"`
	Seniority          string   `json:"seniority,omitempty"           parquet:"seniority,optional"`
	YearsExperience    int      `json:"years_experience,omitempty"    parquet:"years_experience,optional"`
	PrimaryIndustry    string   `json:"primary_industry,omitempty"    parquet:"primary_industry,optional"`
	StrongSkills       []string `json:"strong_skills,omitempty"       parquet:"strong_skills,list,optional"`
	WorkingSkills      []string `json:"working_skills,omitempty"      parquet:"working_skills,list,optional"`
	ToolsFrameworks    []string `json:"tools_frameworks,omitempty"    parquet:"tools_frameworks,list,optional"`
	Certifications     []string `json:"certifications,omitempty"      parquet:"certifications,list,optional"`
	PreferredRoles     []string `json:"preferred_roles,omitempty"     parquet:"preferred_roles,list,optional"`
	Languages          []string `json:"languages,omitempty"           parquet:"languages,list,optional"`
	Education          string   `json:"education,omitempty"           parquet:"education,optional"`
	PreferredLocations []string `json:"preferred_locations,omitempty" parquet:"preferred_locations,list,optional"`
	RemotePreference   string   `json:"remote_preference,omitempty"   parquet:"remote_preference,optional"`
	SalaryMin          int      `json:"salary_min,omitempty"          parquet:"salary_min,optional"`
	SalaryMax          int      `json:"salary_max,omitempty"          parquet:"salary_max,optional"`
	Currency           string   `json:"currency,omitempty"            parquet:"currency,optional"`

	// Score components (matches cv.ScoreComponents).
	ScoreATS      int `json:"score_ats"      parquet:"score_ats"`
	ScoreKeywords int `json:"score_keywords" parquet:"score_keywords"`
	ScoreImpact   int `json:"score_impact"   parquet:"score_impact"`
	ScoreRoleFit  int `json:"score_role_fit" parquet:"score_role_fit"`
	ScoreClarity  int `json:"score_clarity"  parquet:"score_clarity"`
	ScoreOverall  int `json:"score_overall"  parquet:"score_overall"`

	ModelVersionExtract string `json:"model_version_extract,omitempty" parquet:"model_version_extract,optional"`
	ModelVersionScore   string `json:"model_version_score,omitempty"   parquet:"model_version_score,optional"`
}

// CVFix mirrors cv.PriorityFix, carried inside CVImprovedV1.
type CVFix struct {
	FixID          string `json:"fix_id"             parquet:"fix_id"`
	Title          string `json:"title,omitempty"    parquet:"title,optional"`
	ImpactLevel    string `json:"impact,omitempty"   parquet:"impact,optional"`
	Category       string `json:"category,omitempty" parquet:"category,optional"`
	Why            string `json:"why,omitempty"      parquet:"why,optional"`
	AutoApplicable bool   `json:"auto_applicable"    parquet:"auto_applicable"`
	Rewrite        string `json:"rewrite,omitempty"  parquet:"rewrite,optional"`
}

// CVImprovedV1 is emitted by the cv-improve handler after deterministic
// fix detection + LLM rewrite attachment.
type CVImprovedV1 struct {
	CandidateID  string  `json:"candidate_id" parquet:"candidate_id"`
	CVVersion    int     `json:"cv_version"   parquet:"cv_version"`
	Fixes        []CVFix `json:"fixes"        parquet:"fixes,list"`
	ModelVersion string  `json:"model_version,omitempty" parquet:"model_version,optional"`
}

// CandidateEmbeddingV1 is emitted by the cv-embed handler after a
// successful Embed() call on the CV text.
type CandidateEmbeddingV1 struct {
	CandidateID  string    `json:"candidate_id" parquet:"candidate_id"`
	CVVersion    int       `json:"cv_version"   parquet:"cv_version"`
	Vector       []float32 `json:"vector"       parquet:"vector,list"`
	ModelVersion string    `json:"model_version,omitempty" parquet:"model_version,optional"`
}

// PreferencesUpdatedV1 is emitted by the preferences HTTP endpoint.
// It carries the complete preferences set for the candidate — each
// event is a replace-all snapshot, not a delta.
type PreferencesUpdatedV1 struct {
	CandidateID        string   `json:"candidate_id"                  parquet:"candidate_id"`
	RemotePreference   string   `json:"remote_preference,omitempty"   parquet:"remote_preference,optional"`
	SalaryMin          int      `json:"salary_min,omitempty"          parquet:"salary_min,optional"`
	SalaryMax          int      `json:"salary_max,omitempty"          parquet:"salary_max,optional"`
	Currency           string   `json:"currency,omitempty"            parquet:"currency,optional"`
	PreferredLocations []string `json:"preferred_locations,omitempty" parquet:"preferred_locations,list,optional"`
	ExcludedCompanies  []string `json:"excluded_companies,omitempty"  parquet:"excluded_companies,list,optional"`
	TargetRoles        []string `json:"target_roles,omitempty"        parquet:"target_roles,list,optional"`
	Languages          []string `json:"languages,omitempty"           parquet:"languages,list,optional"`
	Availability       string   `json:"availability,omitempty"        parquet:"availability,optional"`
}

// MatchRow is one candidate-to-job match, carried inside MatchesReadyV1.
type MatchRow struct {
	CanonicalID string  `json:"canonical_id"           parquet:"canonical_id"`
	Score       float64 `json:"score"                  parquet:"score"`
	RerankScore float64 `json:"rerank_score,omitempty" parquet:"rerank_score,optional"`
}

// MatchesReadyV1 is emitted by the match endpoint (or the Trustage
// weekly-digest admin) after a candidate's top matches are ranked.
type MatchesReadyV1 struct {
	CandidateID  string     `json:"candidate_id"          parquet:"candidate_id"`
	MatchBatchID string     `json:"match_batch_id"        parquet:"match_batch_id"`
	Matches      []MatchRow `json:"matches"               parquet:"matches,list"`
	OccurredAt   time.Time  `json:"occurred_at,omitempty" parquet:"occurred_at,optional"`
}
