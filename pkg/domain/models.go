package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
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
	BaseModel
	Type             SourceType     `gorm:"type:varchar(50);not null;uniqueIndex:idx_source_type_url" json:"type"`
	Name             string         `gorm:"type:varchar(255)" json:"name"`
	BaseURL          string         `gorm:"type:text;not null;uniqueIndex:idx_source_type_url" json:"base_url"`
	Country          string         `gorm:"type:varchar(10)" json:"country"`
	Language         string         `gorm:"type:varchar(10);not null;default:'en';index" json:"language"`
	Status           SourceStatus   `gorm:"type:varchar(20);not null;default:'active'" json:"status"`
	Priority         Priority       `gorm:"type:smallint;not null;default:1" json:"priority"`
	CrawlIntervalSec int            `gorm:"not null;default:3600" json:"crawl_interval_sec"`
	HealthScore         float64        `gorm:"type:real;not null;default:1.0" json:"health_score"`
	ConsecutiveFailures int            `gorm:"not null;default:0" json:"consecutive_failures"`
	NeedsTuning         bool           `gorm:"not null;default:false" json:"needs_tuning"`
	Config              string         `gorm:"type:jsonb;default:'{}'" json:"config"`
	LastSeenAt       *time.Time     `json:"last_seen_at"`
	NextCrawlAt      time.Time      `gorm:"index" json:"next_crawl_at"`

	// Reachability probe run before every enqueue. Sources that fail
	// repeatedly get their NextCrawlAt pushed out with backoff rather
	// than being dispatched into a pipeline that will just 404.
	LastVerifiedAt    *time.Time `json:"last_verified_at"`
	LastVerifyStatus  int        `gorm:"not null;default:0" json:"last_verify_status"`

	// Source quality sliding window
	QualityWindowStart  *time.Time `json:"quality_window_start"`
	QualityWindowDays   int        `gorm:"not null;default:1" json:"quality_window_days"`
	QualityValidated    int        `gorm:"not null;default:0" json:"quality_validated"`
	QualityFlagged      int        `gorm:"not null;default:0" json:"quality_flagged"`
}

func (Source) TableName() string { return "sources" }

// CrawlJob records a single crawl execution against a source.
type CrawlJob struct {
	BaseModel
	SourceID       string         `gorm:"type:varchar(20);not null;index" json:"source_id"`
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
}

func (CrawlJob) TableName() string { return "crawl_jobs" }

// RawPayload is the metadata row for every HTTP fetch the crawler
// makes. The actual response body lives in R2 at raw/{content_hash}.html.gz
// — this row just records the fetch event and points to it.
type RawPayload struct {
	BaseModel
	CrawlJobID  string    `gorm:"type:varchar(20);not null;index" json:"crawl_job_id"`
	StorageURI  string    `gorm:"type:text" json:"storage_uri"`
	ContentHash string    `gorm:"type:varchar(64);index" json:"content_hash"`
	SizeBytes   int64     `gorm:"not null;default:0" json:"size_bytes"`
	FetchedAt   time.Time `gorm:"not null" json:"fetched_at"`
	HTTPStatus  int       `gorm:"not null" json:"http_status"`
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

// RawRef is the reference-count row linking a raw content-hash
// (R2 raw/{hash}.html.gz) to the variants that use it. Written
// by the canonical handler when it promotes a variant into a
// cluster; deleted by the purge sweeper once the owning cluster
// is torn down. When a hash's ref count drops to zero, the raw
// blob is GC'd from R2.
type RawRef struct {
	BaseModel
	ContentHash string `gorm:"type:varchar(64);not null;uniqueIndex:idx_raw_refs_hash_variant" json:"content_hash"`
	ClusterID   string `gorm:"type:varchar(20);not null;index" json:"cluster_id"`
	VariantID   string `gorm:"type:varchar(20);not null;uniqueIndex:idx_raw_refs_hash_variant" json:"variant_id"`
}

func (RawRef) TableName() string { return "raw_refs" }

// CrawlRequest is published to the queue to trigger a crawl.
type CrawlRequest struct {
	SourceID     string     `json:"source_id"`
	SourceType   SourceType `json:"source_type"`
	ScheduledFor time.Time  `json:"scheduled_for"`
	Attempt      int        `json:"attempt"`
	Priority     Priority   `json:"priority"`
}

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
func BuildSlug(title, company string, id string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s", company, title, id)))
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
