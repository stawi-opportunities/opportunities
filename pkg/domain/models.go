package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// SourceType identifies the connector used to crawl a source.
type SourceType string

const (
	// Free JSON API connectors
	SourceRemoteOK  SourceType = "remoteok"
	SourceArbeitnow SourceType = "arbeitnow"
	SourceJobicy    SourceType = "jobicy"
	SourceTheMuse   SourceType = "themuse"
	SourceHimalayas SourceType = "himalayas"
	SourceFindwork  SourceType = "findwork"

	// African job board connectors
	SourceBrighterMonday SourceType = "brightermonday"
	SourceJobberman      SourceType = "jobberman"
	SourceMyJobMag       SourceType = "myjobmag"
	SourceNjorku         SourceType = "njorku"
	SourceCareers24      SourceType = "careers24"
	SourcePNet           SourceType = "pnet"

	// Existing connectors
	SourceGreenhouse          SourceType = "greenhouse"
	SourceLever               SourceType = "lever"
	SourceWorkday             SourceType = "workday"
	SourceSmartRecruitersAPI  SourceType = "smartrecruiters_api"
	SourceSmartRecruitersPage SourceType = "smartrecruiters_page"
	SourceSchemaOrg           SourceType = "schema_org"
	SourceSitemap             SourceType = "sitemap"
	SourceHostedBoards        SourceType = "hosted_boards"
	SourceGenericHTML         SourceType = "generic_html"
)

// SourceStatus tracks the operational state of a source.
type SourceStatus string

const (
	SourceActive   SourceStatus = "active"
	SourceDegraded SourceStatus = "degraded"
	SourcePaused   SourceStatus = "paused"
	SourceBlocked  SourceStatus = "blocked"
	SourceDisabled SourceStatus = "disabled"
)

// CrawlJobStatus tracks the lifecycle of a crawl job.
type CrawlJobStatus string

const (
	CrawlScheduled CrawlJobStatus = "scheduled"
	CrawlRunning   CrawlJobStatus = "running"
	CrawlSucceeded CrawlJobStatus = "succeeded"
	CrawlFailed    CrawlJobStatus = "failed"
)

// Priority controls scheduling order for crawl jobs.
type Priority int

const (
	PriorityLow    Priority = 0
	PriorityNormal Priority = 1
	PriorityHigh   Priority = 2
	PriorityUrgent Priority = 3
)

// Source represents a configured job board or careers page to crawl.
type Source struct {
	ID               int64          `gorm:"primaryKey;autoIncrement" json:"id"`
	Type             SourceType     `gorm:"type:varchar(50);not null;uniqueIndex:idx_source_type_url" json:"type"`
	Name             string         `gorm:"type:varchar(255)" json:"name"`
	BaseURL          string         `gorm:"type:text;not null;uniqueIndex:idx_source_type_url" json:"base_url"`
	Country          string         `gorm:"type:varchar(10)" json:"country"`
	Status           SourceStatus   `gorm:"type:varchar(20);not null;default:'active'" json:"status"`
	Priority         Priority       `gorm:"type:smallint;not null;default:1" json:"priority"`
	CrawlIntervalSec int            `gorm:"not null;default:3600" json:"crawl_interval_sec"`
	HealthScore         float64        `gorm:"type:real;not null;default:1.0" json:"health_score"`
	ConsecutiveFailures int            `gorm:"not null;default:0" json:"consecutive_failures"`
	NeedsTuning         bool           `gorm:"not null;default:false" json:"needs_tuning"`
	Config              string         `gorm:"type:jsonb;default:'{}'" json:"config"`
	LastSeenAt       *time.Time     `json:"last_seen_at"`
	NextCrawlAt      time.Time      `gorm:"index" json:"next_crawl_at"`

	// Source quality sliding window
	QualityWindowStart  *time.Time `json:"quality_window_start"`
	QualityWindowDays   int        `gorm:"not null;default:1" json:"quality_window_days"`
	QualityValidated    int        `gorm:"not null;default:0" json:"quality_validated"`
	QualityFlagged      int        `gorm:"not null;default:0" json:"quality_flagged"`

	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	DeletedAt        gorm.DeletedAt `gorm:"index" json:"deleted_at"`
}

func (Source) TableName() string { return "sources" }

// CrawlJob records a single crawl execution against a source.
type CrawlJob struct {
	ID             int64          `gorm:"primaryKey;autoIncrement" json:"id"`
	SourceID       int64          `gorm:"not null;index" json:"source_id"`
	ScheduledAt    time.Time      `gorm:"not null" json:"scheduled_at"`
	StartedAt      *time.Time     `json:"started_at"`
	FinishedAt     *time.Time     `json:"finished_at"`
	Status         CrawlJobStatus `gorm:"type:varchar(20);not null;default:'scheduled'" json:"status"`
	Attempt        int            `gorm:"not null;default:1" json:"attempt"`
	IdempotencyKey string         `gorm:"type:varchar(255);uniqueIndex" json:"idempotency_key"`
	ErrorCode      string         `gorm:"type:text" json:"error_code"`
	ErrorMessage   string         `gorm:"type:text" json:"error_message"`
	JobsFound      int            `gorm:"not null;default:0" json:"jobs_found"`
	JobsStored     int            `gorm:"not null;default:0" json:"jobs_stored"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

func (CrawlJob) TableName() string { return "crawl_jobs" }

// RawPayload stores the raw HTTP response from a crawl.
type RawPayload struct {
	ID          int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	CrawlJobID  int64     `gorm:"not null;index" json:"crawl_job_id"`
	StorageURI  string    `gorm:"type:text" json:"storage_uri"`
	ContentHash string    `gorm:"type:varchar(64);index" json:"content_hash"`
	FetchedAt   time.Time `gorm:"not null" json:"fetched_at"`
	HTTPStatus  int       `gorm:"not null" json:"http_status"`
	Body        []byte    `gorm:"type:bytea" json:"-"`
}

func (RawPayload) TableName() string { return "raw_payloads" }

// ExternalJob is the normalized representation extracted from a raw payload
// before deduplication. It is not persisted directly.
type ExternalJob struct {
	ExternalID     string            `json:"external_id"`
	SourceURL      string            `json:"source_url"`
	ApplyURL       string            `json:"apply_url"`
	Title          string            `json:"title"`
	Company        string            `json:"company"`
	LocationText   string            `json:"location_text"`
	RemoteType     string            `json:"remote_type"`
	EmploymentType string            `json:"employment_type"`
	SalaryMin      float64           `json:"salary_min"`
	SalaryMax      float64           `json:"salary_max"`
	Currency       string            `json:"currency"`
	Description    string            `json:"description"`
	PostedAt       *time.Time        `json:"posted_at"`
	Metadata       map[string]string `json:"metadata"`
	// Extended fields extracted by AI
	Seniority    string   `json:"seniority"`
	Skills       []string `json:"skills"`
	Roles        []string `json:"roles"`
	Benefits     []string `json:"benefits"`
	ContactName  string   `json:"contact_name"`
	ContactEmail string   `json:"contact_email"`
	Department   string   `json:"department"`
	Industry     string   `json:"industry"`
	Education    string   `json:"education"`
	Experience   string   `json:"experience"`
	Deadline     string   `json:"deadline"`

	// Intelligence fields
	UrgencyLevel     string   `json:"urgency_level"`
	UrgencySignals   []string `json:"urgency_signals"`
	HiringTimeline   string   `json:"hiring_timeline"`
	InterviewStages  int      `json:"interview_stages"`
	HasTakeHome      bool     `json:"has_take_home"`
	FunnelComplexity string   `json:"funnel_complexity"`
	CompanySize      string   `json:"company_size"`
	FundingStage     string   `json:"funding_stage"`
	RequiredSkills   []string `json:"required_skills"`
	NiceToHaveSkills []string `json:"nice_to_have_skills"`
	ToolsFrameworks  []string `json:"tools_frameworks"`
	GeoRestrictions  string   `json:"geo_restrictions"`
	TimezoneReq      string   `json:"timezone_req"`
	ApplicationType  string   `json:"application_type"`
	ATSPlatform      string   `json:"ats_platform"`
	RoleScope        string   `json:"role_scope"`
	TeamSize         string   `json:"team_size"`
	ReportsTo        string   `json:"reports_to"`
}

// VariantStage tracks a job variant's position in the processing pipeline.
type VariantStage string

const (
	StageRaw        VariantStage = "raw"
	StageDeduped    VariantStage = "deduped"
	StageNormalized VariantStage = "normalized"
	StageValidated  VariantStage = "validated"
	StageReady      VariantStage = "ready"
	StageFlagged    VariantStage = "flagged"
)

// JobVariant represents one observed posting of a job from a specific source.
type JobVariant struct {
	ID             int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	ExternalJobID  string    `gorm:"type:varchar(255);not null;uniqueIndex:idx_variant_source_ext" json:"external_job_id"`
	SourceID       int64     `gorm:"not null;index;uniqueIndex:idx_variant_source_ext" json:"source_id"`
	HardKey        string    `gorm:"type:varchar(64);index" json:"hard_key"`
	SourceURL      string    `gorm:"type:text" json:"source_url"`
	ApplyURL       string    `gorm:"type:text" json:"apply_url"`
	Title          string    `gorm:"type:text;not null" json:"title"`
	Company        string    `gorm:"type:varchar(255);not null" json:"company"`
	LocationText   string    `gorm:"type:text" json:"location_text"`
	Country        string    `gorm:"type:varchar(10)" json:"country"`
	RemoteType     string    `gorm:"type:varchar(20)" json:"remote_type"`
	EmploymentType string    `gorm:"type:varchar(30)" json:"employment_type"`
	SalaryMin      float64   `gorm:"type:real" json:"salary_min"`
	SalaryMax      float64   `gorm:"type:real" json:"salary_max"`
	Currency       string    `gorm:"type:varchar(10)" json:"currency"`
	Description    string    `gorm:"type:text" json:"description"`
	Seniority      string    `gorm:"type:varchar(30)" json:"seniority"`
	Skills         string    `gorm:"type:text" json:"skills"`
	Roles          string    `gorm:"type:text" json:"roles"`
	Benefits       string    `gorm:"type:text" json:"benefits"`
	ContactName    string    `gorm:"type:varchar(255)" json:"contact_name"`
	ContactEmail   string    `gorm:"type:varchar(255)" json:"contact_email"`
	Department     string    `gorm:"type:varchar(255)" json:"department"`
	Industry       string    `gorm:"type:varchar(100)" json:"industry"`
	Education      string    `gorm:"type:text" json:"education"`
	Experience     string    `gorm:"type:varchar(100)" json:"experience"`
	Deadline         string    `gorm:"type:varchar(100)" json:"deadline"`
	UrgencyLevel     string    `gorm:"type:varchar(20)" json:"urgency_level"`
	UrgencySignals   string    `gorm:"type:text" json:"urgency_signals"`
	HiringTimeline   string    `gorm:"type:varchar(30)" json:"hiring_timeline"`
	InterviewStages  int       `gorm:"type:int" json:"interview_stages"`
	HasTakeHome      bool      `gorm:"type:bool" json:"has_take_home"`
	FunnelComplexity string    `gorm:"type:varchar(20)" json:"funnel_complexity"`
	CompanySize      string    `gorm:"type:varchar(20)" json:"company_size"`
	FundingStage     string    `gorm:"type:varchar(20)" json:"funding_stage"`
	RequiredSkills   string    `gorm:"type:text" json:"required_skills"`
	NiceToHaveSkills string    `gorm:"type:text" json:"nice_to_have_skills"`
	ToolsFrameworks  string    `gorm:"type:text" json:"tools_frameworks"`
	GeoRestrictions  string    `gorm:"type:varchar(30)" json:"geo_restrictions"`
	TimezoneReq      string    `gorm:"type:varchar(30)" json:"timezone_req"`
	ApplicationType  string    `gorm:"type:varchar(20)" json:"application_type"`
	ATSPlatform      string    `gorm:"type:varchar(30)" json:"ats_platform"`
	RoleScope        string    `gorm:"type:varchar(20)" json:"role_scope"`
	TeamSize         string    `gorm:"type:varchar(20)" json:"team_size"`
	ReportsTo        string    `gorm:"type:varchar(100)" json:"reports_to"`
	PostedAt         *time.Time `json:"posted_at"`
	ScrapedAt        time.Time  `gorm:"not null" json:"scraped_at"`
	ContentHash      string    `gorm:"type:varchar(64)" json:"content_hash"`

	// Pipeline stage tracking
	Stage           VariantStage `gorm:"type:varchar(20);not null;default:'raw';index" json:"stage"`

	// Stored content forms (for reprocessing without recrawling)
	RawHTML         string  `gorm:"type:text" json:"-"`
	CleanHTML       string  `gorm:"type:text" json:"-"`
	Markdown        string  `gorm:"type:text" json:"-"`

	// Validation (populated in Stage 3)
	ValidationScore *float64 `gorm:"type:real" json:"validation_score"`
	ValidationNotes string   `gorm:"type:text" json:"validation_notes"`

	// Source discovery (populated in Stage 2)
	DiscoveredURLs  string  `gorm:"type:text" json:"discovered_urls"`

	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

func (JobVariant) TableName() string { return "job_variants" }

// JobCluster groups duplicate job variants together.
type JobCluster struct {
	ID                 int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	CanonicalVariantID int64     `gorm:"not null" json:"canonical_variant_id"`
	Confidence         float64   `gorm:"type:real;not null" json:"confidence"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

func (JobCluster) TableName() string { return "job_clusters" }

// JobClusterMember links a variant to a cluster with match metadata.
type JobClusterMember struct {
	ID        int64   `gorm:"primaryKey;autoIncrement" json:"id"`
	ClusterID int64   `gorm:"not null;index" json:"cluster_id"`
	VariantID int64   `gorm:"not null;index" json:"variant_id"`
	MatchType string  `gorm:"type:varchar(20);not null" json:"match_type"`
	Score     float64 `gorm:"type:real;not null" json:"score"`
}

func (JobClusterMember) TableName() string { return "job_cluster_members" }

// CanonicalJob is the deduplicated, canonical representation of a job posting.
type CanonicalJob struct {
	ID             int64      `gorm:"primaryKey;autoIncrement" json:"id"`
	ClusterID      int64      `gorm:"not null;uniqueIndex" json:"cluster_id"`
	Slug           string     `gorm:"type:varchar(255);uniqueIndex" json:"slug"`
	Title          string     `gorm:"type:text;not null" json:"title"`
	Company        string     `gorm:"type:varchar(255);not null" json:"company"`
	Description    string     `gorm:"type:text" json:"description"`
	LocationText   string     `gorm:"type:text" json:"location_text"`
	Country        string     `gorm:"type:varchar(10)" json:"country"`
	RemoteType     string     `gorm:"type:varchar(20)" json:"remote_type"`
	EmploymentType string     `gorm:"type:varchar(30)" json:"employment_type"`
	SalaryMin      float64    `gorm:"type:real" json:"salary_min"`
	SalaryMax      float64    `gorm:"type:real" json:"salary_max"`
	Currency       string     `gorm:"type:varchar(10)" json:"currency"`
	ApplyURL       string     `gorm:"type:text" json:"apply_url"`
	Seniority      string     `gorm:"type:varchar(30)" json:"seniority"`
	Skills         string     `gorm:"type:text" json:"skills"`
	Roles          string     `gorm:"type:text" json:"roles"`
	Benefits       string     `gorm:"type:text" json:"benefits"`
	ContactName    string     `gorm:"type:varchar(255)" json:"contact_name"`
	ContactEmail   string     `gorm:"type:varchar(255)" json:"contact_email"`
	Department     string     `gorm:"type:varchar(255)" json:"department"`
	Industry       string     `gorm:"type:varchar(100)" json:"industry"`
	Education      string     `gorm:"type:text" json:"education"`
	Experience     string     `gorm:"type:varchar(100)" json:"experience"`
	Deadline         string     `gorm:"type:varchar(100)" json:"deadline"`
	UrgencyLevel     string     `gorm:"type:varchar(20)" json:"urgency_level"`
	HiringTimeline   string     `gorm:"type:varchar(30)" json:"hiring_timeline"`
	FunnelComplexity string     `gorm:"type:varchar(20)" json:"funnel_complexity"`
	CompanySize      string     `gorm:"type:varchar(20)" json:"company_size"`
	FundingStage     string     `gorm:"type:varchar(20)" json:"funding_stage"`
	RequiredSkills   string     `gorm:"type:text" json:"required_skills"`
	NiceToHaveSkills string     `gorm:"type:text" json:"nice_to_have_skills"`
	ToolsFrameworks  string     `gorm:"type:text" json:"tools_frameworks"`
	GeoRestrictions  string     `gorm:"type:varchar(30)" json:"geo_restrictions"`
	TimezoneReq      string     `gorm:"type:varchar(30)" json:"timezone_req"`
	ApplicationType  string     `gorm:"type:varchar(20)" json:"application_type"`
	ATSPlatform      string     `gorm:"type:varchar(30)" json:"ats_platform"`
	RoleScope        string     `gorm:"type:varchar(20)" json:"role_scope"`
	QualityScore     float64    `gorm:"type:real;index" json:"quality_score"`
	PostedAt         *time.Time `json:"posted_at"`
	FirstSeenAt      time.Time  `gorm:"not null" json:"first_seen_at"`
	LastSeenAt       time.Time  `gorm:"not null" json:"last_seen_at"`
	Status         string     `gorm:"type:text;not null;default:'active';index" json:"status"`
	ExpiresAt      *time.Time `json:"expires_at"`
	PublishedAt    *time.Time `json:"published_at"`
	R2Version      int        `gorm:"not null;default:0" json:"r2_version"`
	Category       string     `gorm:"type:text;index" json:"category"`
	SearchVector   string     `gorm:"->;type:tsvector" json:"-"`
	Embedding      string     `gorm:"type:text" json:"-"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func (CanonicalJob) TableName() string { return "canonical_jobs" }

// CrawlPageState tracks pagination state for multi-page crawls.
type CrawlPageState struct {
	ID         int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	CrawlJobID int64     `gorm:"not null;index" json:"crawl_job_id"`
	PageURL    string    `gorm:"type:text;not null" json:"page_url"`
	PageNum    int       `gorm:"not null" json:"page_num"`
	CursorNext string    `gorm:"type:text" json:"cursor_next"`
	FetchedAt  time.Time `gorm:"not null" json:"fetched_at"`
	JobCount   int       `gorm:"not null;default:0" json:"job_count"`
}

func (CrawlPageState) TableName() string { return "crawl_page_states" }

// RejectedJob records jobs that were discarded during normalization or dedup.
type RejectedJob struct {
	ID          int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	CrawlJobID  int64     `gorm:"not null;index" json:"crawl_job_id"`
	SourceID    int64     `gorm:"not null;index" json:"source_id"`
	ExternalID  string    `gorm:"type:varchar(255)" json:"external_id"`
	Reason      string    `gorm:"type:varchar(100);not null" json:"reason"`
	RawSnippet  string    `gorm:"type:text" json:"raw_snippet"`
	RejectedAt  time.Time `gorm:"not null" json:"rejected_at"`
}

func (RejectedJob) TableName() string { return "rejected_jobs" }

// CrawlRequest is published to the queue to trigger a crawl.
type CrawlRequest struct {
	SourceID     int64      `json:"source_id"`
	SourceType   SourceType `json:"source_type"`
	ScheduledFor time.Time  `json:"scheduled_for"`
	Attempt      int        `json:"attempt"`
	Priority     Priority   `json:"priority"`
}

// SavedJob records a job that a candidate has bookmarked.
type SavedJob struct {
	ID             int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	ProfileID      string    `gorm:"type:varchar(255);not null;index;uniqueIndex:idx_saved_profile_job" json:"profile_id"`
	CanonicalJobID int64     `gorm:"not null;index;uniqueIndex:idx_saved_profile_job" json:"canonical_job_id"`
	SavedAt        time.Time `gorm:"not null" json:"saved_at"`
}

func (SavedJob) TableName() string { return "saved_jobs" }

// JobCategory classifies a job into a broad functional area.
type JobCategory string

const (
	CategoryProgramming     JobCategory = "programming"
	CategoryDesign          JobCategory = "design"
	CategoryCustomerSupport JobCategory = "customer-support"
	CategoryMarketing       JobCategory = "marketing"
	CategorySales           JobCategory = "sales"
	CategoryDevOps          JobCategory = "devops"
	CategoryProduct         JobCategory = "product"
	CategoryData            JobCategory = "data"
	CategoryManagement      JobCategory = "management"
	CategoryOther           JobCategory = "other"
)

// DeriveCategory infers a JobCategory from role and industry text.
func DeriveCategory(roles, industry string) JobCategory {
	lower := strings.ToLower(roles + " " + industry)
	switch {
	case containsAny(lower, "developer", "engineer", "programmer", "software", "backend", "frontend", "full-stack", "fullstack"):
		return CategoryProgramming
	case containsAny(lower, "designer", "ux", "ui", "graphic", "visual"):
		return CategoryDesign
	case containsAny(lower, "support", "customer success", "customer service", "help desk"):
		return CategoryCustomerSupport
	case containsAny(lower, "marketing", "growth", "seo", "content", "social media"):
		return CategoryMarketing
	case containsAny(lower, "sales", "account executive", "business development", "revenue"):
		return CategorySales
	case containsAny(lower, "devops", "sre", "infrastructure", "platform", "reliability"):
		return CategoryDevOps
	case containsAny(lower, "product manager", "product owner", "product lead"):
		return CategoryProduct
	case containsAny(lower, "data scientist", "data engineer", "analyst", "machine learning", "ai"):
		return CategoryData
	case containsAny(lower, "manager", "director", "vp", "head of", "chief", "lead"):
		return CategoryManagement
	default:
		return CategoryOther
	}
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// Slugify converts a string into a URL-safe slug.
func Slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	replacer := strings.NewReplacer(
		" ", "-", "/", "-", "\\", "-", ".", "-",
		",", "", "'", "", "\"", "", "(", "", ")", "",
		"&", "and", "+", "plus",
	)
	s = replacer.Replace(s)
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}

// BuildSlug creates a permanent, unique, human-readable slug for a canonical job.
// Format: {title}-at-{company}-{6-char-hash}
// The hash is deterministic: SHA256(company|title|id), first 6 hex chars.
func BuildSlug(title, company string, id int64) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d", company, title, id)))
	shortHash := hex.EncodeToString(h[:3])
	slug := fmt.Sprintf("%s-at-%s-%s", Slugify(title), Slugify(company), shortHash)
	if len(slug) > 250 {
		slug = slug[:250]
	}
	return slug
}

// NormalizeToken lowercases and collapses punctuation and whitespace in a string.
func NormalizeToken(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	replacer := strings.NewReplacer(",", " ", ".", " ", "/", " ", "-", " ", "_", " ", "(", "", ")", "")
	s = replacer.Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// BuildHardKey produces a deterministic SHA-256 key from job identity fields.
func BuildHardKey(company, title, location, postingID string) string {
	parts := []string{NormalizeToken(company), NormalizeToken(title), NormalizeToken(location), NormalizeToken(postingID)}
	raw := strings.Join(parts, "|")
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}
