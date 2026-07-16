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

	// CV storage — binary in the platform files service; we keep only a
	// reference here. CVStorageURI is the files media id (preferred) or
	// archive key fallback; CVUrl is the content URI for download/display;
	// CVContentHash is sha256 of the uploaded bytes for change detection.
	// Extracted text is not stored on the profile — it is folded into the
	// placement summary (candidate_placement_profiles) for matching/chat.
	CVUrl         string `gorm:"type:text" json:"cv_url"`
	CVStorageURI  string `gorm:"type:text" json:"-"` // file id
	CVContentHash string `gorm:"type:varchar(64)" json:"-"`

	// AI-extracted profile fields
	CurrentTitle    string         `gorm:"type:text" json:"current_title"`
	Seniority       string         `gorm:"type:varchar(30)" json:"seniority"`
	YearsExperience int            `gorm:"type:int" json:"years_experience"`
	Skills          pq.StringArray `gorm:"type:text[]" json:"skills"`
	StrongSkills    pq.StringArray `gorm:"type:text[]" json:"strong_skills"`
	WorkingSkills   pq.StringArray `gorm:"type:text[]" json:"working_skills"`
	ToolsFrameworks pq.StringArray `gorm:"type:text[]" json:"tools_frameworks"`
	Certifications  string         `gorm:"type:text" json:"certifications"`
	PreferredRoles  string         `gorm:"type:text" json:"preferred_roles"`
	Industries      string         `gorm:"type:text" json:"industries"`
	Education       string         `gorm:"type:text" json:"education"`

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

	// Subscription billing (links to service-payment / product ledger)
	SubscriptionID string `gorm:"type:varchar(255)" json:"subscription_id"`
	PlanID         string `gorm:"type:varchar(100)" json:"plan_id"`
	// CurrentPeriodEnd is when the paid period ends (renewal or cancel effective).
	CurrentPeriodEnd *time.Time `json:"current_period_end,omitempty"`
	// CancelAtPeriodEnd means the user cancelled; stay paid until CurrentPeriodEnd.
	CancelAtPeriodEnd bool `gorm:"not null;default:false" json:"cancel_at_period_end"`

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
	CVScore         int        `gorm:"not null;default:0" json:"cv_score"`
	CVReportJSON    string     `gorm:"type:jsonb;default:'{}'" json:"-"`
	CVScoredAt      *time.Time `json:"cv_scored_at"`
	CVScoredVersion string     `gorm:"type:varchar(32)" json:"cv_scored_version"`

	// OnboardingDraft persists the multi-step wizard state between
	// sessions. The wizard writes here on every step; POST
	// /candidates/onboard promotes the draft into the canonical
	// profile columns and resets this to '{}'.
	OnboardingDraft string `gorm:"type:jsonb;not null;default:'{}'" json:"-"`
}

func (CandidateProfile) TableName() string { return "candidate_profiles" }

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
