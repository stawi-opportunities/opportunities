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

// ExternalOpportunity is the canonical intermediate representation a
// connector returns for a single ingested item. Replaces the old
// ExternalJob; the kind discriminator decides which Spec governs
// extraction, verification, and downstream rendering.
type ExternalOpportunity struct {
	// Discriminator. Empty when the source declares zero kinds — the
	// extractor classifies before downstream stages run.
	Kind string `json:"kind,omitempty"`

	// Source identity
	SourceID   string `json:"source_id"`
	ExternalID string `json:"external_id"`
	SourceURL  string `json:"source_url,omitempty"`

	// Universal core
	Title         string `json:"title"`
	Description   string `json:"description"`
	IssuingEntity string `json:"issuing_entity"`
	ApplyURL      string `json:"apply_url"`

	// Universal location
	AnchorLocation *Location `json:"anchor_location,omitempty"`
	LocationText   string    `json:"location_text,omitempty"`
	Remote         bool      `json:"remote,omitempty"`
	GeoScope       string    `json:"geo_scope,omitempty"` // "global" | "regional" | "national" | "local" | ""

	// Universal time
	PostedAt *time.Time `json:"posted_at,omitempty"`
	Deadline *time.Time `json:"deadline,omitempty"`

	// Universal monetary (semantics determined by Spec.AmountKind)
	AmountMin float64 `json:"amount_min,omitempty"`
	AmountMax float64 `json:"amount_max,omitempty"`
	Currency  string  `json:"currency,omitempty"`

	// Universal taxonomy (validated against Spec.Categories)
	Categories []string `json:"categories,omitempty"`

	// Kind-specific extension. Keys must satisfy Spec.KindRequired and
	// may include Spec.KindOptional. Anything else is flagged by Verify
	// (warning) but not rejected.
	Attributes map[string]any `json:"attributes,omitempty"`

	// Pipeline metadata (kind-agnostic)
	RawHTML         string     `json:"raw_html,omitempty"`
	RawHash         string     `json:"raw_hash,omitempty"`
	ContentMarkdown string     `json:"content_markdown,omitempty"`
	Source          SourceType `json:"source"`
	QualityScore    float64    `json:"quality_score,omitempty"`
}

// AttrString returns Attributes[key] as a string, or "" if missing/wrong type.
func (o ExternalOpportunity) AttrString(key string) string {
	if o.Attributes == nil {
		return ""
	}
	if v, ok := o.Attributes[key].(string); ok {
		return v
	}
	return ""
}

// AttrStringSlice returns Attributes[key] as a []string, or nil.
func (o ExternalOpportunity) AttrStringSlice(key string) []string {
	if o.Attributes == nil {
		return nil
	}
	switch v := o.Attributes[key].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// AttrFloat returns Attributes[key] as a float64, or 0.
func (o ExternalOpportunity) AttrFloat(key string) float64 {
	if o.Attributes == nil {
		return 0
	}
	switch v := o.Attributes[key].(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
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
