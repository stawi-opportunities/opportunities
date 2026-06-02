package domain

import (
	"time"

	"github.com/lib/pq"
)

// CandidateStatus tracks the lifecycle state of a candidate profile.
type CandidateStatus string

const (
	CandidateUnverified CandidateStatus = "unverified"
	CandidateActive     CandidateStatus = "active"
	CandidatePaused     CandidateStatus = "paused"
	CandidateHired      CandidateStatus = "hired"
)

// SubscriptionTier represents the billing tier for a candidate.
type SubscriptionTier string

const (
	SubscriptionFree      SubscriptionTier = "free"
	SubscriptionTrial     SubscriptionTier = "trial"
	SubscriptionPaid      SubscriptionTier = "paid"
	SubscriptionCancelled SubscriptionTier = "cancelled"
)

// MatchStatus tracks the delivery and engagement state of a job match.
type MatchStatus string

const (
	MatchNew      MatchStatus = "new"
	MatchSent     MatchStatus = "sent"
	MatchViewed   MatchStatus = "viewed"
	MatchApplied  MatchStatus = "applied"
	MatchRejected MatchStatus = "rejected"
	MatchHired    MatchStatus = "hired"
)

// CandidateProfile stores all data for a registered job seeker.
type CandidateProfile struct {
	BaseModel
	ProfileID    string           `gorm:"type:varchar(255);index" json:"profile_id"`
	Status       CandidateStatus  `gorm:"type:varchar(20);not null;default:'unverified'" json:"status"`
	Subscription SubscriptionTier `gorm:"type:varchar(20);not null;default:'free'" json:"subscription"`
	AutoApply    bool             `gorm:"not null;default:false" json:"auto_apply"`

	// CV storage
	CVUrl string `gorm:"type:text" json:"cv_url"`
	// CV durable storage — raw text lives in R2 at
	// candidates/{candidate_id}/cv-raw.txt.gz. The row carries the
	// pointer + sha256 for change-detection. Both empty until the
	// CV-upload path is rewired through R2 (follow-up; the columns
	// are pre-created so the next iteration can land without
	// another migration).
	CVStorageURI  string `gorm:"type:text" json:"-"`
	CVContentHash string `gorm:"type:varchar(64)" json:"-"`

	// AI-extracted profile fields
	CurrentTitle    string         `gorm:"type:text" json:"current_title"`
	Seniority       string         `gorm:"type:varchar(30)" json:"seniority"`
	YearsExperience int            `gorm:"type:int" json:"years_experience"`
	Skills          pq.StringArray `gorm:"type:text[]" json:"skills"`
	StrongSkills    pq.StringArray `gorm:"type:text[]" json:"strong_skills"`
	WorkingSkills   pq.StringArray `gorm:"type:text[]" json:"working_skills"`
	ToolsFrameworks pq.StringArray `gorm:"type:text[]" json:"tools_frameworks"`
	Certifications  string `gorm:"type:text" json:"certifications"`
	PreferredRoles  string `gorm:"type:text" json:"preferred_roles"`
	Industries      string `gorm:"type:text" json:"industries"`
	Education       string `gorm:"type:text" json:"education"`

	// Job preferences
	PreferredLocations string  `gorm:"type:text" json:"preferred_locations"`
	PreferredCountries string  `gorm:"type:text" json:"preferred_countries"`
	RemotePreference   string  `gorm:"type:varchar(20)" json:"remote_preference"`
	SalaryMin          float32 `gorm:"type:real" json:"salary_min"`
	SalaryMax          float32 `gorm:"type:real" json:"salary_max"`
	Currency           string  `gorm:"type:varchar(10)" json:"currency"`

	// Onboarding domain fields
	TargetJobTitle     string `gorm:"type:text" json:"target_job_title"`
	ExperienceLevel    string `gorm:"type:varchar(30)" json:"experience_level"`
	JobSearchStatus    string `gorm:"type:varchar(30)" json:"job_search_status"`
	PreferredRegions   string `gorm:"type:text" json:"preferred_regions"`
	PreferredTimezones string `gorm:"type:text" json:"preferred_timezones"`
	USWorkAuth         *bool  `gorm:"type:bool" json:"us_work_auth"`
	NeedsSponsorship   *bool  `gorm:"type:bool" json:"needs_sponsorship"`
	WantsATSReport     bool   `gorm:"not null;default:false" json:"wants_ats_report"`

	// Subscription billing (links to service-payment)
	SubscriptionID string `gorm:"type:varchar(255)" json:"subscription_id"`
	PlanID         string `gorm:"type:varchar(100)" json:"plan_id"`

	// Additional profile fields
	Languages   string `gorm:"type:text" json:"languages"`
	Bio         string `gorm:"type:text" json:"bio"`
	WorkHistory string `gorm:"type:jsonb;default:'[]'" json:"work_history"`

	// Communication channel preferences (not contact details)
	CommEmail    bool `gorm:"not null;default:true" json:"comm_email"`
	CommWhatsapp bool `gorm:"not null;default:false" json:"comm_whatsapp"`
	CommTelegram bool `gorm:"not null;default:false" json:"comm_telegram"`
	CommSMS      bool `gorm:"not null;default:false" json:"comm_sms"`

	// Matching metadata
	MatchesSent     int        `gorm:"not null;default:0" json:"matches_sent"`
	LastMatchedAt   *time.Time `json:"last_matched_at"`
	LastContactedAt *time.Time `json:"last_contacted_at"`

	// CV Strength Report cache. Populated on every CV upload/update
	// and re-scoring round, so the dashboard's "Your CV Strength"
	// panel has an authoritative number without a fresh LLM round
	// trip on each page load. Empty CVReportJSON means the candidate
	// has never been scored (or the CV couldn't be parsed).
	CVScore           int        `gorm:"not null;default:0" json:"cv_score"`
	CVReportJSON      string     `gorm:"type:jsonb;default:'{}'" json:"-"`
	CVScoredAt        *time.Time `json:"cv_scored_at"`
	CVScoredVersion   string     `gorm:"type:varchar(32)" json:"cv_scored_version"`

	// OnboardingDraft persists the multi-step wizard state between
	// sessions. The wizard writes here on every step; POST
	// /candidates/onboard promotes the draft into the canonical
	// profile columns and resets this to '{}'.
	OnboardingDraft string `gorm:"type:jsonb;not null;default:'{}'" json:"-"`
}

func (CandidateProfile) TableName() string { return "candidate_profiles" }

// CandidateMatch records a scored pairing between a candidate and a canonical job.
//
// MatchScore is the final score used for ordering; it's the reranker score
// when available, otherwise the bi-encoder EmbeddingSimilarity. Keeping the
// upstream signals on the row makes it trivial to diff "before/after
// reranker" in observability without re-running the cron.
//
// NOTE: main retired this type in PR #6 (manticore/match-table cleanup).
// We retain it on feature/auto-apply because the event-driven auto-apply
// pipeline (apps/autoapply, apps/matching/service/events/v1/auto_apply.go)
// still depends on it. Consolidation onto main's apps/applications HTTP
// CRUD model is tracked as a follow-up.
type CandidateMatch struct {
	BaseModel
	CandidateID         string      `gorm:"type:varchar(20);not null;index;uniqueIndex:idx_candidate_job" json:"candidate_id"`
	CanonicalJobID      string      `gorm:"type:varchar(20);not null;index;uniqueIndex:idx_candidate_job" json:"canonical_job_id"`
	MatchScore          float32     `gorm:"type:real;not null" json:"match_score"`
	SkillsOverlap       float32     `gorm:"type:real" json:"skills_overlap"`
	EmbeddingSimilarity float32     `gorm:"type:real" json:"embedding_similarity"`

	// Reranker metadata (nullable — v1 ships with flag off).
	RerankScore      *float32   `gorm:"type:real;index:,where:rerank_score IS NOT NULL,sort:desc" json:"rerank_score,omitempty"`
	RetrievalScore   *float32   `gorm:"type:real" json:"retrieval_score,omitempty"`
	RetrievalRank    *int       `gorm:"type:int" json:"retrieval_rank,omitempty"`
	RerankerVersion  string     `gorm:"type:varchar(64)" json:"reranker_version,omitempty"`
	RerankedAt       *time.Time `json:"reranked_at,omitempty"`

	Status    MatchStatus `gorm:"type:varchar(20);not null;default:'new'" json:"status"`
	SentAt    *time.Time  `json:"sent_at"`
	ViewedAt  *time.Time  `json:"viewed_at"`
	AppliedAt *time.Time  `json:"applied_at"`
}

func (CandidateMatch) TableName() string { return "candidate_matches" }

// Application status constants for CandidateApplication.Status. Retained
// alongside CandidateMatch above for the same reason — the event-driven
// autoapply handler reads these.
const (
	AppStatusPending   = "pending"
	AppStatusSubmitted = "submitted"
	AppStatusFailed    = "failed"
	AppStatusSkipped   = "skipped"
)

// CandidateApplication records a job application submitted by or on behalf of a candidate.
type CandidateApplication struct {
	BaseModel
	CandidateID    string     `gorm:"type:varchar(20);not null;index" json:"candidate_id"`
	MatchID        *string    `gorm:"type:varchar(20);index" json:"match_id"`
	CanonicalJobID string     `gorm:"type:varchar(20);not null;index" json:"canonical_job_id"`
	Method         string     `gorm:"type:varchar(20)" json:"method"`
	Status         string     `gorm:"type:varchar(20);not null;default:'pending'" json:"status"`
	ApplyURL       string     `gorm:"type:text" json:"apply_url"`
	CoverLetter    string     `gorm:"type:text" json:"cover_letter"`
	SubmittedAt    *time.Time `json:"submitted_at"`
	ResponseAt     *time.Time `json:"response_at"`
	ResponseType   string     `gorm:"type:varchar(20)" json:"response_type"`
}

func (CandidateApplication) TableName() string { return "candidate_applications" }

// Capture-origin constants for CandidateSession.CaptureOrigin.
const (
	SessionOriginExtension     = "extension"
	SessionOriginRemoteBrowser = "remote_browser"
)

// CandidateSession holds an authenticated session captured from a candidate's
// real browser (via the extension) so the autoapply pipeline can submit
// applications to sources that gate behind login (BrighterMonday, LinkedIn,
// Workday tenants, …).
//
// Payload is envelope-encrypted: PayloadEnc is AES-256-GCM(plaintext, DEK),
// the DEK is AES-256-GCM-wrapped under a master key, KeyID identifies that
// master (so rotation re-wraps DEKs without re-encrypting PayloadEnc).
type CandidateSession struct {
	BaseModel
	CandidateID string     `gorm:"type:varchar(20);not null;index" json:"candidate_id"`
	SourceType  SourceType `gorm:"type:varchar(40);not null"        json:"source_type"`

	PayloadEnc   []byte `gorm:"type:bytea;not null" json:"-"`
	PayloadNonce []byte `gorm:"type:bytea;not null" json:"-"`
	DEKWrapped   []byte `gorm:"type:bytea;not null" json:"-"`
	DEKNonce     []byte `gorm:"type:bytea;not null" json:"-"`
	KeyID        string `gorm:"type:varchar(64);not null" json:"key_id"`

	CapturedAt    time.Time  `gorm:"not null"            json:"captured_at"`
	ExpiresAt     *time.Time `                           json:"expires_at,omitempty"`
	LastUsedAt    *time.Time `                           json:"last_used_at,omitempty"`
	RevokedAt     *time.Time `                           json:"revoked_at,omitempty"`
	UserAgent     string     `gorm:"type:varchar(255);not null;default:''" json:"user_agent"`
	CaptureOrigin string     `gorm:"type:varchar(40);not null;default:'extension'" json:"capture_origin"`
}

func (CandidateSession) TableName() string { return "candidate_sessions" }

// SessionPayload is the plaintext shape of an encrypted session row. It is
// never persisted — the Store marshals it to JSON, encrypts, and writes the
// ciphertext to PayloadEnc; on load the ciphertext is decrypted and
// unmarshaled back into this struct.
type SessionPayload struct {
	Cookies []SessionCookie   `json:"cookies"`
	Headers map[string]string `json:"headers,omitempty"`
	Storage map[string]string `json:"storage,omitempty"`
}

// SessionCookie mirrors net/http.Cookie's persisted fields. We keep our own
// type so we control the wire format independent of stdlib changes and so
// JSON marshaling stays explicit.
type SessionCookie struct {
	Name     string     `json:"name"`
	Value    string     `json:"value"`
	Domain   string     `json:"domain,omitempty"`
	Path     string     `json:"path,omitempty"`
	Expires  *time.Time `json:"expires,omitempty"`
	HTTPOnly bool       `json:"http_only,omitempty"`
	Secure   bool       `json:"secure,omitempty"`
	SameSite string     `json:"same_site,omitempty"`
}
