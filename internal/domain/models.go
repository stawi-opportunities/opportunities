package domain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

type SourceType string

const (
	SourceAdzuna              SourceType = "adzuna"
	SourceSerpAPI             SourceType = "serpapi"
	SourceUSAJobs             SourceType = "usajobs"
	SourceSmartRecruitersAPI  SourceType = "smartrecruiters_api"
	SourceGreenhouse          SourceType = "greenhouse"
	SourceLever               SourceType = "lever"
	SourceWorkday             SourceType = "workday"
	SourceSmartRecruitersPage SourceType = "smartrecruiters_page"
	SourceSchemaOrg           SourceType = "schema_org"
	SourceSitemap             SourceType = "sitemap"
	SourceHostedBoards        SourceType = "hosted_boards"
	SourceGenericHTML         SourceType = "generic_html"
)

type SourceStatus string

const (
	SourceActive   SourceStatus = "active"
	SourcePaused   SourceStatus = "paused"
	SourceBlocked  SourceStatus = "blocked"
	SourceDisabled SourceStatus = "disabled"
)

type Source struct {
	ID               int64
	Type             SourceType
	BaseURL          string
	Country          string
	Status           SourceStatus
	CrawlIntervalSec int
	HealthScore      float64
	LastSeenAt       *time.Time
	NextCrawlAt      time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type CrawlJobStatus string

const (
	CrawlScheduled CrawlJobStatus = "scheduled"
	CrawlRunning   CrawlJobStatus = "running"
	CrawlSucceeded CrawlJobStatus = "succeeded"
	CrawlFailed    CrawlJobStatus = "failed"
)

type CrawlJob struct {
	ID             int64
	SourceID       int64
	ScheduledAt    time.Time
	StartedAt      *time.Time
	FinishedAt     *time.Time
	Status         CrawlJobStatus
	Attempt        int
	IdempotencyKey string
	ErrorCode      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type RawPayload struct {
	ID          int64
	CrawlJobID  int64
	StorageURI  string
	ContentHash string
	FetchedAt   time.Time
	HTTPStatus  int
	Body        []byte
}

type ExternalJob struct {
	ExternalID     string
	SourceURL      string
	ApplyURL       string
	Title          string
	Company        string
	LocationText   string
	RemoteType     string
	EmploymentType string
	SalaryMin      float64
	SalaryMax      float64
	Currency       string
	Description    string
	PostedAt       *time.Time
	Metadata       map[string]string
}

type JobVariant struct {
	ID             int64
	ExternalJobID  string
	SourceID       int64
	SourceURL      string
	ApplyURL       string
	Title          string
	Company        string
	LocationText   string
	Country        string
	RemoteType     string
	EmploymentType string
	SalaryMin      float64
	SalaryMax      float64
	Currency       string
	Description    string
	PostedAt       *time.Time
	ScrapedAt      time.Time
	ContentHash    string
}

type JobCluster struct {
	ID                 int64
	CanonicalVariantID int64
	Confidence         float64
	UpdatedAt          time.Time
}

type CanonicalJob struct {
	ID             int64
	ClusterID      int64
	Title          string
	Company        string
	Description    string
	LocationText   string
	Country        string
	RemoteType     string
	EmploymentType string
	SalaryMin      float64
	SalaryMax      float64
	Currency       string
	ApplyURL       string
	PostedAt       *time.Time
	FirstSeenAt    time.Time
	LastSeenAt     time.Time
	IsActive       bool
}

type CrawlRequest struct {
	SourceID     int64      `json:"source_id"`
	SourceType   SourceType `json:"source_type"`
	ScheduledFor time.Time  `json:"scheduled_for"`
	Attempt      int        `json:"attempt"`
}

func NormalizeToken(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	replacer := strings.NewReplacer(",", " ", ".", " ", "/", " ", "-", " ", "_", " ", "(", "", ")", "")
	s = replacer.Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

func BuildHardKey(company, title, location, postingID string) string {
	parts := []string{NormalizeToken(company), NormalizeToken(title), NormalizeToken(location), NormalizeToken(postingID)}
	raw := strings.Join(parts, "|")
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

type SourceStore interface {
	UpsertSource(ctx context.Context, src Source) (Source, error)
	ListDueSources(ctx context.Context, now time.Time, limit int) ([]Source, error)
	ListSources(ctx context.Context, limit int) ([]Source, error)
	GetSource(ctx context.Context, id int64) (Source, error)
	TouchSource(ctx context.Context, id int64, next time.Time, health float64) error
}

type CrawlStore interface {
	CreateCrawlJob(ctx context.Context, job CrawlJob) (CrawlJob, error)
	StartCrawlJob(ctx context.Context, id int64, startedAt time.Time) error
	FinishCrawlJob(ctx context.Context, id int64, status CrawlJobStatus, finishedAt time.Time, errCode string) error
	StoreRawPayload(ctx context.Context, payload RawPayload) (RawPayload, error)
}

type JobStore interface {
	UpsertVariant(ctx context.Context, variant JobVariant) (JobVariant, error)
	GetVariantByHardKey(ctx context.Context, hardKey string) (*JobVariant, error)
	BindVariantToCluster(ctx context.Context, variantID, clusterID int64, matchType string, score float64) error
	CreateCluster(ctx context.Context, canonicalVariantID int64, confidence float64) (JobCluster, error)
	UpdateCanonicalJob(ctx context.Context, canonical CanonicalJob) error
	SearchCanonicalJobs(ctx context.Context, query string, limit int) ([]CanonicalJob, error)
}

type UnitOfWork interface {
	SourceStore
	CrawlStore
	JobStore
}
