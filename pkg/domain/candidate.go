package domain

import (
	"time"

	"gorm.io/gorm"
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
	ID           int64            `gorm:"primaryKey;autoIncrement" json:"id"`
	ProfileID    string           `gorm:"type:varchar(255);index" json:"profile_id"`
	Status       CandidateStatus  `gorm:"type:varchar(20);not null;default:'unverified'" json:"status"`
	Subscription SubscriptionTier `gorm:"type:varchar(20);not null;default:'free'" json:"subscription"`
	AutoApply    bool             `gorm:"not null;default:false" json:"auto_apply"`

	// CV storage
	CVUrl     string `gorm:"type:text" json:"cv_url"`
	CVRawText string `gorm:"type:text" json:"-"`

	// AI-extracted profile fields
	CurrentTitle    string `gorm:"type:text" json:"current_title"`
	Seniority       string `gorm:"type:varchar(30)" json:"seniority"`
	YearsExperience int    `gorm:"type:int" json:"years_experience"`
	Skills          string `gorm:"type:text" json:"skills"`
	StrongSkills    string `gorm:"type:text" json:"strong_skills"`
	WorkingSkills   string `gorm:"type:text" json:"working_skills"`
	ToolsFrameworks string `gorm:"type:text" json:"tools_frameworks"`
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
	Embedding       string     `gorm:"type:text" json:"-"`
	MatchesSent     int        `gorm:"not null;default:0" json:"matches_sent"`
	LastMatchedAt   *time.Time `json:"last_matched_at"`
	LastContactedAt *time.Time `json:"last_contacted_at"`

	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at"`
}

func (CandidateProfile) TableName() string { return "candidate_profiles" }

// CandidateMatch records a scored pairing between a candidate and a canonical job.
//
// MatchScore is the final score used for ordering; it's the reranker score
// when available, otherwise the bi-encoder EmbeddingSimilarity. Keeping the
// upstream signals on the row makes it trivial to diff "before/after
// reranker" in observability without re-running the cron.
type CandidateMatch struct {
	ID                  int64       `gorm:"primaryKey;autoIncrement" json:"id"`
	CandidateID         int64       `gorm:"not null;index;uniqueIndex:idx_candidate_job" json:"candidate_id"`
	CanonicalJobID      int64       `gorm:"not null;index;uniqueIndex:idx_candidate_job" json:"canonical_job_id"`
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
	CreatedAt time.Time   `json:"created_at"`
}

func (CandidateMatch) TableName() string { return "candidate_matches" }

// RerankCache stores reranker scores keyed by a content hash so we don't pay
// for re-scoring the same (CV, job) pair across weekly cron sweeps.
//
// CacheKey is sha256(cv_text | canonical_job_id | reranker_version) hex-
// encoded — stable as long as the CV and job haven't changed.
type RerankCache struct {
	CacheKey        string    `gorm:"type:varchar(64);primaryKey" json:"cache_key"`
	Score           float32   `gorm:"type:real;not null" json:"score"`
	RerankerVersion string    `gorm:"type:varchar(64);not null" json:"reranker_version"`
	CreatedAt       time.Time `gorm:"index:,where:created_at < NOW() - INTERVAL '30 days'" json:"created_at"`
	Hits            int       `gorm:"type:int;not null;default:0" json:"hits"`
}

func (RerankCache) TableName() string { return "rerank_cache" }

// CandidateApplication records a job application submitted by or on behalf of a candidate.
type CandidateApplication struct {
	ID             int64      `gorm:"primaryKey;autoIncrement" json:"id"`
	CandidateID    int64      `gorm:"not null;index" json:"candidate_id"`
	MatchID        *int64     `gorm:"index" json:"match_id"`
	CanonicalJobID int64      `gorm:"not null;index" json:"canonical_job_id"`
	Method         string     `gorm:"type:varchar(20)" json:"method"`
	Status         string     `gorm:"type:varchar(20);not null;default:'pending'" json:"status"`
	ApplyURL       string     `gorm:"type:text" json:"apply_url"`
	CoverLetter    string     `gorm:"type:text" json:"cover_letter"`
	SubmittedAt    *time.Time `json:"submitted_at"`
	ResponseAt     *time.Time `json:"response_at"`
	ResponseType   string     `gorm:"type:varchar(20)" json:"response_type"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func (CandidateApplication) TableName() string { return "candidate_applications" }
