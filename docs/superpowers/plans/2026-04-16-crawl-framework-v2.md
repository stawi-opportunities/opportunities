# Crawl Framework V2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild the stawi.opportunities crawling framework on Frame, add free JSON API and African board connectors, deploy to Kubernetes, and crawl quality global jobs into PostgreSQL.

**Architecture:** Three consolidated services (crawler, scheduler, api) built on the Frame framework with NATS JetStream for messaging, PostgreSQL via Frame's datastore for persistence, and an iterator-based connector pipeline with quality gates. Deployed via FluxCD + colony Helm chart to an existing Kubernetes cluster.

**Tech Stack:** Go 1.26, Frame (pitabwire/frame), NATS JetStream (pitabwire/natspubsub), PostgreSQL (GORM via Frame datastore), chi HTTP router, OpenTelemetry via Frame, colony Helm chart, FluxCD

---

## File Structure

### Project Root Changes
```
stawi.opportunities/
├── apps/
│   ├── crawler/
│   │   ├── Dockerfile
│   │   ├── cmd/
│   │   │   └── main.go
│   │   ├── config/
│   │   │   └── config.go
│   │   ├── migrations/
│   │   │   └── (sql files)
│   │   └── service/
│   │       └── setup.go
│   ├── scheduler/
│   │   ├── Dockerfile
│   │   ├── cmd/
│   │   │   └── main.go
│   │   ├── config/
│   │   │   └── config.go
│   │   └── service/
│   │       └── setup.go
│   └── api/
│       ├── Dockerfile
│       ├── cmd/
│       │   └── main.go
│       ├── config/
│       │   └── config.go
│       └── service/
│           └── setup.go
├── pkg/
│   ├── domain/
│   │   └── models.go
│   ├── repository/
│   │   ├── source.go
│   │   ├── crawl.go
│   │   ├── job.go
│   │   └── rejected.go
│   ├── connectors/
│   │   ├── connector.go          ← interface + registry + iterator
│   │   ├── httpx/
│   │   │   └── client.go
│   │   ├── remoteok/
│   │   │   └── remoteok.go
│   │   ├── arbeitnow/
│   │   │   └── arbeitnow.go
│   │   ├── jobicy/
│   │   │   └── jobicy.go
│   │   ├── themuse/
│   │   │   └── themuse.go
│   │   ├── himalayas/
│   │   │   └── himalayas.go
│   │   ├── findwork/
│   │   │   └── findwork.go
│   │   ├── brightermonday/
│   │   │   └── brightermonday.go
│   │   ├── jobberman/
│   │   │   └── jobberman.go
│   │   ├── myjobmag/
│   │   │   └── myjobmag.go
│   │   ├── njorku/
│   │   │   └── njorku.go
│   │   ├── careers24/
│   │   │   └── careers24.go
│   │   ├── pnet/
│   │   │   └── pnet.go
│   │   ├── greenhouse/
│   │   │   └── greenhouse.go
│   │   ├── lever/
│   │   │   └── lever.go
│   │   ├── workday/
│   │   │   └── workday.go
│   │   ├── smartrecruiters/
│   │   │   └── smartrecruiters.go
│   │   ├── smartrecruiterspage/
│   │   │   └── smartrecruiterspage.go
│   │   ├── schemaorg/
│   │   │   └── schemaorg.go
│   │   ├── sitemap/
│   │   │   └── sitemap.go
│   │   ├── hostedboards/
│   │   │   └── hostedboards.go
│   │   └── generichtml/
│   │       └── generichtml.go
│   ├── normalize/
│   │   ├── normalize.go
│   │   └── normalize_test.go
│   ├── dedupe/
│   │   ├── dedupe.go
│   │   └── dedupe_test.go
│   ├── quality/
│   │   ├── gate.go
│   │   └── gate_test.go
│   ├── pipeline/
│   │   ├── worker.go
│   │   └── batch.go
│   └── seeds/
│       └── loader.go
├── seeds/
│   ├── apis.json
│   ├── africa/
│   │   ├── ke.json
│   │   ├── ng.json
│   │   ├── za.json
│   │   ├── gh.json
│   │   ├── ug.json
│   │   └── pan-african.json
│   ├── europe/
│   │   └── uk.json
│   ├── oceania/
│   │   └── au.json
│   ├── greenhouse_boards.json
│   ├── lever_boards.json
│   └── workday_sites.json
├── .dockerignore
├── Makefile
├── go.mod
└── go.sum
```

### Deployment Repo (antinvestor/deployments)
```
manifests/namespaces/opportunities/
├── namespace.yaml
├── kustomization.yaml
├── kustomization_provider.yaml
├── common/
│   ├── kustomization.yaml
│   ├── flux-image-automation.yaml
│   ├── image_repository_secret.yaml
│   ├── reference-grant.yaml
│   └── setup_queue.yaml
├── crawler/
│   ├── kustomization.yaml
│   ├── opportunities-crawler.yaml
│   ├── database.yaml
│   ├── db-credentials.yaml
│   └── queue_setup.yaml
├── scheduler/
│   ├── kustomization.yaml
│   ├── opportunities-scheduler.yaml
│   └── db-credentials.yaml
└── api/
    ├── kustomization.yaml
    ├── opportunities-api.yaml
    └── db-credentials.yaml
```

---

## Task 1: Project Restructure — Move from cmd/ to apps/pkg/

**Files:**
- Create: `pkg/domain/models.go`
- Create: `apps/crawler/cmd/main.go` (placeholder)
- Create: `apps/scheduler/cmd/main.go` (placeholder)
- Create: `apps/api/cmd/main.go` (placeholder)
- Delete: `cmd/` directory (after migration)
- Delete: `internal/` directory (after migration to pkg/)

- [ ] **Step 1: Create pkg/domain/models.go from existing internal/domain/models.go**

Copy the domain models, updating the package path from `internal/domain` to `pkg/domain`. Keep all existing types but add the new fields from the spec.

```go
// pkg/domain/models.go
package domain

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SourceType identifies the connector type for a source.
type SourceType string

const (
	SourceTypeRemoteOK             SourceType = "remoteok"
	SourceTypeArbeitnow            SourceType = "arbeitnow"
	SourceTypeJobicy               SourceType = "jobicy"
	SourceTypeTheMuse              SourceType = "themuse"
	SourceTypeHimalayas            SourceType = "himalayas"
	SourceTypeFindWork             SourceType = "findwork"
	SourceTypeBrighterMonday       SourceType = "brightermonday"
	SourceTypeJobberman            SourceType = "jobberman"
	SourceTypeMyJobMag             SourceType = "myjobmag"
	SourceTypeNjorku               SourceType = "njorku"
	SourceTypeCareers24            SourceType = "careers24"
	SourceTypePNet                 SourceType = "pnet"
	SourceTypeGreenhouse           SourceType = "greenhouse"
	SourceTypeLever                SourceType = "lever"
	SourceTypeWorkday              SourceType = "workday"
	SourceTypeSmartRecruitersAPI   SourceType = "smartrecruiters_api"
	SourceTypeSmartRecruitersPage  SourceType = "smartrecruiters_page"
	SourceTypeSchemaOrg            SourceType = "schema_org"
	SourceTypeSitemap              SourceType = "sitemap"
	SourceTypeHostedBoards         SourceType = "hosted_boards"
	SourceTypeGenericHTML          SourceType = "generic_html"
)

type SourceStatus string

const (
	SourceStatusActive   SourceStatus = "active"
	SourceStatusPaused   SourceStatus = "paused"
	SourceStatusBlocked  SourceStatus = "blocked"
	SourceStatusDisabled SourceStatus = "disabled"
)

type CrawlJobStatus string

const (
	CrawlJobScheduled CrawlJobStatus = "scheduled"
	CrawlJobRunning   CrawlJobStatus = "running"
	CrawlJobSucceeded CrawlJobStatus = "succeeded"
	CrawlJobFailed    CrawlJobStatus = "failed"
)

type Priority string

const (
	PriorityHot    Priority = "hot"
	PriorityNormal Priority = "normal"
	PriorityCold   Priority = "cold"
)

type Source struct {
	ID               int64            `json:"id" gorm:"primaryKey;autoIncrement"`
	SourceType       SourceType       `json:"source_type" gorm:"type:text;not null"`
	BaseURL          string           `json:"base_url" gorm:"type:text;not null"`
	Country          string           `json:"country" gorm:"type:text"`
	Region           string           `json:"region" gorm:"type:text"`
	Status           SourceStatus     `json:"status" gorm:"type:text;default:'active'"`
	Priority         Priority         `json:"priority" gorm:"type:text;default:'normal'"`
	CrawlIntervalSec int              `json:"crawl_interval_sec" gorm:"default:21600"`
	HealthScore      float64          `json:"health_score" gorm:"default:1.0"`
	CrawlCursor      json.RawMessage  `json:"crawl_cursor" gorm:"type:jsonb;default:'{}'"`
	LastSeenAt       *time.Time       `json:"last_seen_at"`
	NextCrawlAt      *time.Time       `json:"next_crawl_at"`
	CreatedAt        time.Time        `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt        time.Time        `json:"updated_at" gorm:"autoUpdateTime"`
}

func (Source) TableName() string { return "sources" }

type CrawlJob struct {
	ID             int64          `json:"id" gorm:"primaryKey;autoIncrement"`
	SourceID       int64          `json:"source_id" gorm:"not null"`
	ScheduledAt    time.Time      `json:"scheduled_at"`
	StartedAt      *time.Time     `json:"started_at"`
	FinishedAt     *time.Time     `json:"finished_at"`
	Status         CrawlJobStatus `json:"status" gorm:"type:text;default:'scheduled'"`
	Attempt        int            `json:"attempt" gorm:"default:1"`
	IdempotencyKey string         `json:"idempotency_key" gorm:"type:text;uniqueIndex"`
	ErrorCode      string         `json:"error_code" gorm:"type:text"`
	CreatedAt      time.Time      `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt      time.Time      `json:"updated_at" gorm:"autoUpdateTime"`
}

func (CrawlJob) TableName() string { return "crawl_jobs" }

type RawPayload struct {
	ID         int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	CrawlJobID int64    `json:"crawl_job_id"`
	StorageURI string   `json:"storage_uri" gorm:"type:text"`
	ContentHash string  `json:"content_hash" gorm:"type:text"`
	FetchedAt  time.Time `json:"fetched_at"`
	HTTPStatus int       `json:"http_status"`
	Body       []byte    `json:"body" gorm:"type:bytea"`
	CreatedAt  time.Time `json:"created_at" gorm:"autoCreateTime"`
}

func (RawPayload) TableName() string { return "raw_payloads" }

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
}

type JobVariant struct {
	ID             int64      `json:"id" gorm:"primaryKey;autoIncrement"`
	ExternalJobID  string     `json:"external_job_id" gorm:"type:text"`
	SourceID       int64      `json:"source_id" gorm:"not null"`
	SourceURL      string     `json:"source_url" gorm:"type:text"`
	ApplyURL       string     `json:"apply_url" gorm:"type:text"`
	Title          string     `json:"title" gorm:"type:text"`
	Company        string     `json:"company" gorm:"type:text"`
	LocationText   string     `json:"location_text" gorm:"type:text"`
	Country        string     `json:"country" gorm:"type:text"`
	Region         string     `json:"region" gorm:"type:text"`
	Language       string     `json:"language" gorm:"type:text"`
	SourceBoard    string     `json:"source_board" gorm:"type:text"`
	RemoteType     string     `json:"remote_type" gorm:"type:text"`
	EmploymentType string     `json:"employment_type" gorm:"type:text"`
	SalaryMin      float64    `json:"salary_min"`
	SalaryMax      float64    `json:"salary_max"`
	Currency       string     `json:"currency" gorm:"type:text"`
	DescriptionText string   `json:"description_text" gorm:"type:text"`
	PostedAt       *time.Time `json:"posted_at"`
	ScrapedAt      time.Time  `json:"scraped_at"`
	ContentHash    string     `json:"content_hash" gorm:"type:text"`
	HardKey        string     `json:"hard_key" gorm:"type:text;index"`
	CreatedAt      time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt      time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
}

func (JobVariant) TableName() string { return "job_variants" }

type JobCluster struct {
	ID                 int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	CanonicalVariantID int64     `json:"canonical_variant_id"`
	Confidence         float64   `json:"confidence"`
	CreatedAt          time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt          time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

func (JobCluster) TableName() string { return "job_clusters" }

type JobClusterMember struct {
	ClusterID int64     `json:"cluster_id" gorm:"primaryKey"`
	VariantID int64     `json:"variant_id" gorm:"primaryKey"`
	MatchType string    `json:"match_type" gorm:"type:text"`
	Score     float64   `json:"score"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

func (JobClusterMember) TableName() string { return "job_cluster_members" }

type CanonicalJob struct {
	ID             int64      `json:"id" gorm:"primaryKey;autoIncrement"`
	ClusterID      int64      `json:"cluster_id" gorm:"uniqueIndex"`
	Title          string     `json:"title" gorm:"type:text"`
	Company        string     `json:"company" gorm:"type:text"`
	DescriptionText string   `json:"description_text" gorm:"type:text"`
	LocationText   string     `json:"location_text" gorm:"type:text"`
	Country        string     `json:"country" gorm:"type:text"`
	RemoteType     string     `json:"remote_type" gorm:"type:text"`
	EmploymentType string     `json:"employment_type" gorm:"type:text"`
	SalaryMin      float64    `json:"salary_min"`
	SalaryMax      float64    `json:"salary_max"`
	Currency       string     `json:"currency" gorm:"type:text"`
	ApplyURL       string     `json:"apply_url" gorm:"type:text"`
	PostedAt       *time.Time `json:"posted_at"`
	FirstSeenAt    time.Time  `json:"first_seen_at"`
	LastSeenAt     time.Time  `json:"last_seen_at"`
	IsActive       bool       `json:"is_active" gorm:"default:true"`
	CreatedAt      time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt      time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
}

func (CanonicalJob) TableName() string { return "canonical_jobs" }

type CrawlPageState struct {
	SourceID    int64     `json:"source_id" gorm:"primaryKey"`
	PageKey     string    `json:"page_key" gorm:"primaryKey;type:text"`
	ContentHash string    `json:"content_hash" gorm:"type:text;not null"`
	LastSeenAt  time.Time `json:"last_seen_at" gorm:"not null"`
}

func (CrawlPageState) TableName() string { return "crawl_page_state" }

type RejectedJob struct {
	ID            int64           `json:"id" gorm:"primaryKey;autoIncrement"`
	SourceID      int64           `json:"source_id"`
	SourceType    SourceType      `json:"source_type" gorm:"type:text;not null"`
	ExternalJobID string          `json:"external_job_id" gorm:"type:text"`
	Reason        string          `json:"reason" gorm:"type:text;not null"`
	RawData       json.RawMessage `json:"raw_data" gorm:"type:jsonb"`
	RejectedAt    time.Time       `json:"rejected_at" gorm:"autoCreateTime"`
}

func (RejectedJob) TableName() string { return "rejected_jobs" }

type CrawlRequest struct {
	SourceID       int64      `json:"source_id"`
	SourceType     SourceType `json:"source_type"`
	BaseURL        string     `json:"base_url"`
	Country        string     `json:"country"`
	Priority       Priority   `json:"priority"`
	ScheduledFor   time.Time  `json:"scheduled_for"`
	Attempt        int        `json:"attempt"`
	IdempotencyKey string     `json:"idempotency_key"`
}

// NormalizeToken lowercases, trims, and normalizes punctuation.
func NormalizeToken(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Join(strings.Fields(s), " ")
	return s
}

// BuildHardKey produces a SHA-256 hash for deduplication.
func BuildHardKey(company, title, location, postingID string) string {
	raw := NormalizeToken(company) + "|" + NormalizeToken(title) + "|" + NormalizeToken(location) + "|" + NormalizeToken(postingID)
	h := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", h)
}
```

- [ ] **Step 2: Create placeholder app entry points**

Create `apps/crawler/cmd/main.go`:

```go
package main

import "fmt"

func main() {
	fmt.Println("crawler starting")
}
```

Create `apps/scheduler/cmd/main.go`:

```go
package main

import "fmt"

func main() {
	fmt.Println("scheduler starting")
}
```

Create `apps/api/cmd/main.go`:

```go
package main

import "fmt"

func main() {
	fmt.Println("api starting")
}
```

- [ ] **Step 3: Verify the project compiles**

Run: `cd /home/j/code/stawi.opportunities && go build ./...`
Expected: Compiles cleanly (old cmd/ and internal/ still exist — they'll be removed after all code is migrated)

- [ ] **Step 4: Commit**

```bash
git add apps/ pkg/
git commit -m "feat: restructure project to apps/pkg layout with updated domain models"
```

---

## Task 2: Database Migrations (GORM-based)

**Files:**
- Create: `apps/crawler/migrations/0001_init_up.sql`

Frame uses file-based SQL migrations. The crawler service owns all database migrations since it's the primary writer.

- [ ] **Step 1: Write the migration file**

Create `apps/crawler/migrations/0001_init_up.sql`:

```sql
-- Sources: job posting source registry
CREATE TABLE IF NOT EXISTS sources (
    id BIGSERIAL PRIMARY KEY,
    source_type TEXT NOT NULL,
    base_url TEXT NOT NULL,
    country TEXT,
    region TEXT,
    status TEXT NOT NULL DEFAULT 'active',
    priority TEXT NOT NULL DEFAULT 'normal',
    crawl_interval_sec INT NOT NULL DEFAULT 21600,
    health_score DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    crawl_cursor JSONB DEFAULT '{}',
    last_seen_at TIMESTAMPTZ,
    next_crawl_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(source_type, base_url)
);

-- Crawl jobs: execution log per crawl attempt
CREATE TABLE IF NOT EXISTS crawl_jobs (
    id BIGSERIAL PRIMARY KEY,
    source_id BIGINT NOT NULL REFERENCES sources(id),
    scheduled_at TIMESTAMPTZ NOT NULL,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    status TEXT NOT NULL DEFAULT 'scheduled',
    attempt INT NOT NULL DEFAULT 1,
    idempotency_key TEXT UNIQUE,
    error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Raw payloads: audit trail of HTTP responses
CREATE TABLE IF NOT EXISTS raw_payloads (
    id BIGSERIAL PRIMARY KEY,
    crawl_job_id BIGINT REFERENCES crawl_jobs(id),
    storage_uri TEXT,
    content_hash TEXT,
    fetched_at TIMESTAMPTZ NOT NULL,
    http_status INT,
    body BYTEA,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Job variants: normalized external jobs (one per source per job)
CREATE TABLE IF NOT EXISTS job_variants (
    id BIGSERIAL PRIMARY KEY,
    external_job_id TEXT,
    source_id BIGINT NOT NULL REFERENCES sources(id),
    source_url TEXT,
    apply_url TEXT,
    title TEXT,
    company TEXT,
    location_text TEXT,
    country TEXT,
    region TEXT,
    language TEXT,
    source_board TEXT,
    remote_type TEXT,
    employment_type TEXT,
    salary_min DOUBLE PRECISION DEFAULT 0,
    salary_max DOUBLE PRECISION DEFAULT 0,
    currency TEXT,
    description_text TEXT,
    posted_at TIMESTAMPTZ,
    scraped_at TIMESTAMPTZ NOT NULL,
    content_hash TEXT,
    hard_key TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(source_id, external_job_id)
);

CREATE INDEX IF NOT EXISTS idx_job_variants_hard_key ON job_variants(hard_key);
CREATE INDEX IF NOT EXISTS idx_job_variants_scraped_at ON job_variants(scraped_at);

-- Job clusters: groups duplicate variants
CREATE TABLE IF NOT EXISTS job_clusters (
    id BIGSERIAL PRIMARY KEY,
    canonical_variant_id BIGINT REFERENCES job_variants(id),
    confidence DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Cluster membership: M:N between variants and clusters
CREATE TABLE IF NOT EXISTS job_cluster_members (
    cluster_id BIGINT NOT NULL REFERENCES job_clusters(id),
    variant_id BIGINT NOT NULL REFERENCES job_variants(id),
    match_type TEXT NOT NULL,
    score DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY(cluster_id, variant_id)
);

-- Canonical jobs: deduplicated view (one per cluster)
CREATE TABLE IF NOT EXISTS canonical_jobs (
    id BIGSERIAL PRIMARY KEY,
    cluster_id BIGINT UNIQUE REFERENCES job_clusters(id),
    title TEXT,
    company TEXT,
    description_text TEXT,
    location_text TEXT,
    country TEXT,
    remote_type TEXT,
    employment_type TEXT,
    salary_min DOUBLE PRECISION DEFAULT 0,
    salary_max DOUBLE PRECISION DEFAULT 0,
    currency TEXT,
    apply_url TEXT,
    posted_at TIMESTAMPTZ,
    first_seen_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_canonical_jobs_last_seen ON canonical_jobs(last_seen_at DESC);

-- Crawl page state: tracks page-level content hashes for incremental crawling
CREATE TABLE IF NOT EXISTS crawl_page_state (
    source_id BIGINT NOT NULL REFERENCES sources(id),
    page_key TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY(source_id, page_key)
);

-- Rejected jobs: quality gate failures for debugging
CREATE TABLE IF NOT EXISTS rejected_jobs (
    id BIGSERIAL PRIMARY KEY,
    source_id BIGINT REFERENCES sources(id),
    source_type TEXT NOT NULL,
    external_job_id TEXT,
    reason TEXT NOT NULL,
    raw_data JSONB,
    rejected_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

- [ ] **Step 2: Commit**

```bash
git add apps/crawler/migrations/
git commit -m "feat: add database migration for crawl framework v2"
```

---

## Task 3: Repository Layer (Frame Datastore Pattern)

**Files:**
- Create: `pkg/repository/source.go`
- Create: `pkg/repository/crawl.go`
- Create: `pkg/repository/job.go`
- Create: `pkg/repository/rejected.go`

These wrap GORM via Frame's datastore pool pattern.

- [ ] **Step 1: Create source repository**

Create `pkg/repository/source.go`:

```go
package repository

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"stawi.opportunities/pkg/domain"
)

type SourceRepository struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

func NewSourceRepository(dbFunc func(ctx context.Context, readOnly bool) *gorm.DB) *SourceRepository {
	return &SourceRepository{db: dbFunc}
}

func (r *SourceRepository) Upsert(ctx context.Context, s *domain.Source) error {
	return r.db(ctx, false).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "source_type"}, {Name: "base_url"}},
			DoUpdates: clause.AssignmentColumns([]string{"country", "region", "status", "priority", "crawl_interval_sec", "updated_at"}),
		}).
		Create(s).Error
}

func (r *SourceRepository) GetByID(ctx context.Context, id int64) (*domain.Source, error) {
	var s domain.Source
	err := r.db(ctx, true).Where("id = ?", id).First(&s).Error
	return &s, err
}

func (r *SourceRepository) ListDue(ctx context.Context, limit int) ([]domain.Source, error) {
	var sources []domain.Source
	err := r.db(ctx, true).
		Where("status = ? AND (next_crawl_at IS NULL OR next_crawl_at <= ?)", domain.SourceStatusActive, time.Now()).
		Order("next_crawl_at ASC NULLS FIRST").
		Limit(limit).
		Find(&sources).Error
	return sources, err
}

func (r *SourceRepository) ListAll(ctx context.Context, limit int) ([]domain.Source, error) {
	var sources []domain.Source
	err := r.db(ctx, true).Limit(limit).Find(&sources).Error
	return sources, err
}

func (r *SourceRepository) UpdateNextCrawl(ctx context.Context, id int64, next time.Time, healthScore float64) error {
	return r.db(ctx, false).
		Model(&domain.Source{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"next_crawl_at": next,
			"last_seen_at":  time.Now(),
			"health_score":  healthScore,
		}).Error
}

func (r *SourceRepository) UpdateCrawlCursor(ctx context.Context, id int64, cursor []byte) error {
	return r.db(ctx, false).
		Model(&domain.Source{}).
		Where("id = ?", id).
		Update("crawl_cursor", cursor).Error
}

func (r *SourceRepository) MarkBlocked(ctx context.Context, id int64) error {
	return r.db(ctx, false).
		Model(&domain.Source{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":       domain.SourceStatusBlocked,
			"health_score": 0.0,
		}).Error
}

func (r *SourceRepository) Count(ctx context.Context) (int64, error) {
	var count int64
	err := r.db(ctx, true).Model(&domain.Source{}).Where("status = ?", domain.SourceStatusActive).Count(&count).Error
	return count, err
}
```

- [ ] **Step 2: Create crawl job repository**

Create `pkg/repository/crawl.go`:

```go
package repository

import (
	"context"
	"time"

	"gorm.io/gorm"
	"stawi.opportunities/pkg/domain"
)

type CrawlRepository struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

func NewCrawlRepository(dbFunc func(ctx context.Context, readOnly bool) *gorm.DB) *CrawlRepository {
	return &CrawlRepository{db: dbFunc}
}

func (r *CrawlRepository) Create(ctx context.Context, cj *domain.CrawlJob) error {
	return r.db(ctx, false).Create(cj).Error
}

func (r *CrawlRepository) Start(ctx context.Context, id int64) error {
	now := time.Now()
	return r.db(ctx, false).
		Model(&domain.CrawlJob{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":     domain.CrawlJobRunning,
			"started_at": now,
		}).Error
}

func (r *CrawlRepository) Finish(ctx context.Context, id int64, status domain.CrawlJobStatus, errorCode string) error {
	now := time.Now()
	return r.db(ctx, false).
		Model(&domain.CrawlJob{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":      status,
			"finished_at": now,
			"error_code":  errorCode,
		}).Error
}

func (r *CrawlRepository) SaveRawPayload(ctx context.Context, p *domain.RawPayload) error {
	return r.db(ctx, false).Create(p).Error
}
```

- [ ] **Step 3: Create job repository**

Create `pkg/repository/job.go`:

```go
package repository

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"stawi.opportunities/pkg/domain"
)

type JobRepository struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

func NewJobRepository(dbFunc func(ctx context.Context, readOnly bool) *gorm.DB) *JobRepository {
	return &JobRepository{db: dbFunc}
}

func (r *JobRepository) UpsertVariant(ctx context.Context, v *domain.JobVariant) error {
	return r.db(ctx, false).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "source_id"}, {Name: "external_job_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"title", "company", "location_text", "description_text",
				"apply_url", "source_url", "remote_type", "employment_type",
				"salary_min", "salary_max", "currency", "country", "region",
				"language", "source_board", "content_hash", "hard_key",
				"scraped_at", "posted_at", "updated_at",
			}),
		}).
		Create(v).Error
}

func (r *JobRepository) UpsertVariants(ctx context.Context, variants []domain.JobVariant) error {
	if len(variants) == 0 {
		return nil
	}
	return r.db(ctx, false).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "source_id"}, {Name: "external_job_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"title", "company", "location_text", "description_text",
				"apply_url", "source_url", "remote_type", "employment_type",
				"salary_min", "salary_max", "currency", "country", "region",
				"language", "source_board", "content_hash", "hard_key",
				"scraped_at", "posted_at", "updated_at",
			}),
		}).
		CreateInBatches(&variants, 100).Error
}

func (r *JobRepository) FindByHardKey(ctx context.Context, hardKey string) (*domain.JobVariant, error) {
	var v domain.JobVariant
	err := r.db(ctx, true).Where("hard_key = ?", hardKey).First(&v).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &v, err
}

func (r *JobRepository) CreateCluster(ctx context.Context, c *domain.JobCluster) error {
	return r.db(ctx, false).Create(c).Error
}

func (r *JobRepository) AddClusterMember(ctx context.Context, m *domain.JobClusterMember) error {
	return r.db(ctx, false).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(m).Error
}

func (r *JobRepository) UpsertCanonical(ctx context.Context, cj *domain.CanonicalJob) error {
	return r.db(ctx, false).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "cluster_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"title", "company", "description_text", "location_text",
				"country", "remote_type", "employment_type",
				"salary_min", "salary_max", "currency", "apply_url",
				"posted_at", "last_seen_at", "updated_at",
			}),
		}).
		Create(cj).Error
}

func (r *JobRepository) SearchCanonical(ctx context.Context, query string, limit int) ([]domain.CanonicalJob, error) {
	var jobs []domain.CanonicalJob
	db := r.db(ctx, true).Where("is_active = true")
	if query != "" {
		db = db.Where("title ILIKE ? OR company ILIKE ? OR description_text ILIKE ?",
			"%"+query+"%", "%"+query+"%", "%"+query+"%")
	}
	err := db.Order("last_seen_at DESC").Limit(limit).Find(&jobs).Error
	return jobs, err
}

func (r *JobRepository) CountVariants(ctx context.Context) (int64, error) {
	var count int64
	err := r.db(ctx, true).Model(&domain.JobVariant{}).Count(&count).Error
	return count, err
}

func (r *JobRepository) CountCanonical(ctx context.Context) (int64, error) {
	var count int64
	err := r.db(ctx, true).Model(&domain.CanonicalJob{}).Where("is_active = true").Count(&count).Error
	return count, err
}

func (r *JobRepository) CountVariantsByCountry(ctx context.Context) (map[string]int64, error) {
	type result struct {
		Country string
		Count   int64
	}
	var results []result
	err := r.db(ctx, true).
		Model(&domain.JobVariant{}).
		Select("country, count(*) as count").
		Group("country").
		Order("count DESC").
		Find(&results).Error
	if err != nil {
		return nil, err
	}
	m := make(map[string]int64, len(results))
	for _, r := range results {
		m[r.Country] = r.Count
	}
	return m, nil
}

func (r *JobRepository) GetPageState(ctx context.Context, sourceID int64, pageKey string) (*domain.CrawlPageState, error) {
	var ps domain.CrawlPageState
	err := r.db(ctx, true).Where("source_id = ? AND page_key = ?", sourceID, pageKey).First(&ps).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &ps, err
}

func (r *JobRepository) UpsertPageState(ctx context.Context, ps *domain.CrawlPageState) error {
	ps.LastSeenAt = time.Now()
	return r.db(ctx, false).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "source_id"}, {Name: "page_key"}},
			DoUpdates: clause.AssignmentColumns([]string{"content_hash", "last_seen_at"}),
		}).
		Create(ps).Error
}
```

- [ ] **Step 4: Create rejected job repository**

Create `pkg/repository/rejected.go`:

```go
package repository

import (
	"context"

	"gorm.io/gorm"
	"stawi.opportunities/pkg/domain"
)

type RejectedRepository struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

func NewRejectedRepository(dbFunc func(ctx context.Context, readOnly bool) *gorm.DB) *RejectedRepository {
	return &RejectedRepository{db: dbFunc}
}

func (r *RejectedRepository) Create(ctx context.Context, rj *domain.RejectedJob) error {
	return r.db(ctx, false).Create(rj).Error
}

func (r *RejectedRepository) CountBySourceType(ctx context.Context) (map[string]int64, error) {
	type result struct {
		SourceType string
		Count      int64
	}
	var results []result
	err := r.db(ctx, true).
		Model(&domain.RejectedJob{}).
		Select("source_type, count(*) as count").
		Group("source_type").
		Order("count DESC").
		Find(&results).Error
	if err != nil {
		return nil, err
	}
	m := make(map[string]int64, len(results))
	for _, r := range results {
		m[r.SourceType] = r.Count
	}
	return m, nil
}
```

- [ ] **Step 5: Verify compilation**

Run: `cd /home/j/code/stawi.opportunities && go mod tidy && go build ./...`

- [ ] **Step 6: Commit**

```bash
git add pkg/repository/
git commit -m "feat: add repository layer using GORM for all domain entities"
```

---

## Task 4: Quality Gate

**Files:**
- Create: `pkg/quality/gate.go`
- Create: `pkg/quality/gate_test.go`

- [ ] **Step 1: Write the quality gate tests**

Create `pkg/quality/gate_test.go`:

```go
package quality

import (
	"testing"

	"stawi.opportunities/pkg/domain"
)

func validJob() domain.ExternalJob {
	return domain.ExternalJob{
		Title:       "Software Engineer",
		Company:     "Acme Corp",
		LocationText: "Nairobi, Kenya",
		Description: "We are looking for a software engineer to join our team and build amazing products for our customers across Africa.",
		ApplyURL:    "https://example.com/apply/123",
	}
}

func TestValidJobPasses(t *testing.T) {
	err := Check(validJob())
	if err != nil {
		t.Fatalf("expected valid job to pass, got: %v", err)
	}
}

func TestMissingTitle(t *testing.T) {
	j := validJob()
	j.Title = ""
	err := Check(j)
	if err == nil {
		t.Fatal("expected error for missing title")
	}
	if err.Error() != "missing_title" {
		t.Fatalf("expected missing_title, got: %s", err.Error())
	}
}

func TestShortTitle(t *testing.T) {
	j := validJob()
	j.Title = "ab"
	err := Check(j)
	if err == nil {
		t.Fatal("expected error for short title")
	}
}

func TestMissingCompany(t *testing.T) {
	j := validJob()
	j.Company = ""
	err := Check(j)
	if err == nil {
		t.Fatal("expected error for missing company")
	}
}

func TestMissingLocation(t *testing.T) {
	j := validJob()
	j.LocationText = ""
	err := Check(j)
	if err == nil {
		t.Fatal("expected error for missing location")
	}
}

func TestShortDescription(t *testing.T) {
	j := validJob()
	j.Description = "Too short"
	err := Check(j)
	if err == nil {
		t.Fatal("expected error for short description")
	}
}

func TestMissingApplyURL(t *testing.T) {
	j := validJob()
	j.ApplyURL = ""
	err := Check(j)
	if err == nil {
		t.Fatal("expected error for missing apply url")
	}
}

func TestInvalidApplyURL(t *testing.T) {
	j := validJob()
	j.ApplyURL = "not-a-url"
	err := Check(j)
	if err == nil {
		t.Fatal("expected error for invalid apply url")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/j/code/stawi.opportunities && go test ./pkg/quality/...`
Expected: Compilation error (Check function doesn't exist yet)

- [ ] **Step 3: Implement quality gate**

Create `pkg/quality/gate.go`:

```go
package quality

import (
	"errors"
	"strings"

	"stawi.opportunities/pkg/domain"
)

func Check(j domain.ExternalJob) error {
	if strings.TrimSpace(j.Title) == "" || len(strings.TrimSpace(j.Title)) < 3 {
		return errors.New("missing_title")
	}
	if strings.TrimSpace(j.Company) == "" || len(strings.TrimSpace(j.Company)) < 2 {
		return errors.New("missing_company")
	}
	if strings.TrimSpace(j.LocationText) == "" {
		return errors.New("missing_location")
	}
	if len(strings.TrimSpace(j.Description)) < 50 {
		return errors.New("short_description")
	}
	url := strings.TrimSpace(j.ApplyURL)
	if url == "" {
		return errors.New("missing_apply_url")
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return errors.New("invalid_apply_url")
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/j/code/stawi.opportunities && go test ./pkg/quality/... -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/quality/
git commit -m "feat: add quality gate with required field validation"
```

---

## Task 5: Connector Interface + HTTP Client + Registry

**Files:**
- Create: `pkg/connectors/connector.go`
- Create: `pkg/connectors/httpx/client.go`

- [ ] **Step 1: Create iterator-based connector interface and registry**

Create `pkg/connectors/connector.go`:

```go
package connectors

import (
	"context"
	"encoding/json"
	"sync"

	"stawi.opportunities/pkg/domain"
)

// CrawlIterator pages through results from a connector.
type CrawlIterator interface {
	Next(ctx context.Context) bool
	Jobs() []domain.ExternalJob
	RawPayload() []byte
	HTTPStatus() int
	Err() error
	Cursor() json.RawMessage
}

// Connector fetches and parses jobs from an external source.
type Connector interface {
	Type() domain.SourceType
	Crawl(ctx context.Context, source domain.Source) CrawlIterator
}

// Registry maps source types to connector implementations.
type Registry struct {
	mu         sync.RWMutex
	connectors map[domain.SourceType]Connector
}

func NewRegistry() *Registry {
	return &Registry{connectors: make(map[domain.SourceType]Connector)}
}

func (r *Registry) Register(c Connector) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.connectors[c.Type()] = c
}

func (r *Registry) Get(t domain.SourceType) (Connector, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.connectors[t]
	return c, ok
}

func (r *Registry) Types() []domain.SourceType {
	r.mu.RLock()
	defer r.mu.RUnlock()
	types := make([]domain.SourceType, 0, len(r.connectors))
	for t := range r.connectors {
		types = append(types, t)
	}
	return types
}

// SinglePageIterator wraps a single batch of jobs (for non-paginated APIs).
type SinglePageIterator struct {
	jobs    []domain.ExternalJob
	raw     []byte
	status  int
	cursor  json.RawMessage
	err     error
	done    bool
}

func NewSinglePageIterator(jobs []domain.ExternalJob, raw []byte, status int, err error) *SinglePageIterator {
	return &SinglePageIterator{jobs: jobs, raw: raw, status: status, err: err}
}

func (it *SinglePageIterator) Next(_ context.Context) bool {
	if it.done || it.err != nil {
		return false
	}
	it.done = true
	return len(it.jobs) > 0
}

func (it *SinglePageIterator) Jobs() []domain.ExternalJob    { return it.jobs }
func (it *SinglePageIterator) RawPayload() []byte             { return it.raw }
func (it *SinglePageIterator) HTTPStatus() int                { return it.status }
func (it *SinglePageIterator) Err() error                     { return it.err }
func (it *SinglePageIterator) Cursor() json.RawMessage        { return it.cursor }
```

- [ ] **Step 2: Create HTTP client with retries**

Create `pkg/connectors/httpx/client.go`:

```go
package httpx

import (
	"context"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"time"
)

type Client struct {
	client    *http.Client
	userAgent string
}

func NewClient(timeout time.Duration, userAgent string) *Client {
	return &Client{
		client:    &http.Client{Timeout: timeout},
		userAgent: userAgent,
	}
}

func (c *Client) Get(ctx context.Context, url string, headers map[string]string) ([]byte, int, error) {
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(math.Pow(2, float64(attempt))) * time.Second
			jitter := time.Duration(rand.Int63n(int64(backoff / 2)))
			select {
			case <-ctx.Done():
				return nil, 0, ctx.Err()
			case <-time.After(backoff + jitter):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, 0, fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("User-Agent", c.userAgent)
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("http %d from %s", resp.StatusCode, url)
			continue
		}

		return body, resp.StatusCode, nil
	}
	return nil, 0, fmt.Errorf("after 5 retries: %w", lastErr)
}
```

- [ ] **Step 3: Verify compilation**

Run: `cd /home/j/code/stawi.opportunities && go build ./pkg/connectors/...`

- [ ] **Step 4: Commit**

```bash
git add pkg/connectors/connector.go pkg/connectors/httpx/
git commit -m "feat: add iterator-based connector interface, registry, and HTTP client"
```

---

## Task 6: Normalization Enhancements

**Files:**
- Create: `pkg/normalize/normalize.go`
- Create: `pkg/normalize/normalize_test.go`

- [ ] **Step 1: Write normalization tests**

Create `pkg/normalize/normalize_test.go`:

```go
package normalize

import (
	"testing"
	"time"

	"stawi.opportunities/pkg/domain"
)

func TestExternalToVariant(t *testing.T) {
	now := time.Now()
	ext := domain.ExternalJob{
		ExternalID:   "job-123",
		Title:        "  Software Engineer  ",
		Company:      "Acme Corp Ltd",
		LocationText: "Nairobi, Kenya",
		Description:  "Build amazing products",
		ApplyURL:     "https://example.com/apply",
		Currency:     "usd",
		RemoteType:   "HYBRID",
	}

	v := ExternalToVariant(ext, 1, "KE", "brightermonday", now)

	if v.Title != "Software Engineer" {
		t.Errorf("expected trimmed title, got %q", v.Title)
	}
	if v.Company != "Acme Corp" {
		t.Errorf("expected normalized company, got %q", v.Company)
	}
	if v.Country != "KE" {
		t.Errorf("expected KE, got %q", v.Country)
	}
	if v.Currency != "USD" {
		t.Errorf("expected USD, got %q", v.Currency)
	}
	if v.RemoteType != "hybrid" {
		t.Errorf("expected hybrid, got %q", v.RemoteType)
	}
	if v.ContentHash == "" {
		t.Error("expected non-empty content hash")
	}
	if v.HardKey == "" {
		t.Error("expected non-empty hard key")
	}
	if v.SourceBoard != "brightermonday" {
		t.Errorf("expected brightermonday, got %q", v.SourceBoard)
	}
}

func TestGeneratedIDWhenMissing(t *testing.T) {
	ext := domain.ExternalJob{
		Title:   "Test Job",
		Company: "Test Co",
	}
	v := ExternalToVariant(ext, 1, "", "", time.Now())
	if v.ExternalJobID == "" {
		t.Error("expected generated external job id")
	}
}

func TestCompanyNormalization(t *testing.T) {
	cases := []struct {
		input, expected string
	}{
		{"Acme Corp Ltd", "Acme Corp"},
		{"Google Inc.", "Google"},
		{"Safaricom Pty", "Safaricom"},
		{"MTN Group GmbH", "MTN Group"},
		{"  Spaces  Everywhere  ", "Spaces Everywhere"},
	}
	for _, tc := range cases {
		got := normalizeCompany(tc.input)
		if got != tc.expected {
			t.Errorf("normalizeCompany(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestDetectRegion(t *testing.T) {
	cases := []struct {
		country, expected string
	}{
		{"KE", "east_africa"},
		{"NG", "west_africa"},
		{"ZA", "southern_africa"},
		{"EG", "north_africa"},
		{"GB", "europe"},
		{"AU", "oceania"},
		{"US", "americas"},
		{"XX", ""},
	}
	for _, tc := range cases {
		got := DetectRegion(tc.country)
		if got != tc.expected {
			t.Errorf("DetectRegion(%q) = %q, want %q", tc.country, got, tc.expected)
		}
	}
}
```

- [ ] **Step 2: Implement normalization**

Create `pkg/normalize/normalize.go`:

```go
package normalize

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"stawi.opportunities/pkg/domain"
)

var companySuffixes = []string{
	" ltd", " ltd.", " inc", " inc.", " pty", " pty.",
	" gmbh", " llc", " plc", " corp", " corp.",
	" limited", " incorporated",
}

var regionMap = map[string]string{
	// East Africa
	"KE": "east_africa", "UG": "east_africa", "TZ": "east_africa",
	"RW": "east_africa", "ET": "east_africa", "SO": "east_africa",
	// West Africa
	"NG": "west_africa", "GH": "west_africa", "SN": "west_africa",
	"CI": "west_africa", "CM": "west_africa", "ML": "west_africa",
	// Southern Africa
	"ZA": "southern_africa", "ZW": "southern_africa", "BW": "southern_africa",
	"MZ": "southern_africa", "ZM": "southern_africa", "MW": "southern_africa",
	"NA": "southern_africa",
	// North Africa
	"EG": "north_africa", "MA": "north_africa", "TN": "north_africa",
	"DZ": "north_africa", "LY": "north_africa",
	// Europe
	"GB": "europe", "DE": "europe", "FR": "europe", "NL": "europe",
	"IE": "europe", "ES": "europe", "IT": "europe", "SE": "europe",
	"NO": "europe", "DK": "europe", "FI": "europe", "PL": "europe",
	"PT": "europe", "AT": "europe", "CH": "europe", "BE": "europe",
	// Oceania
	"AU": "oceania", "NZ": "oceania",
	// Americas
	"US": "americas", "CA": "americas", "BR": "americas", "MX": "americas",
	// Asia
	"IN": "asia", "SG": "asia", "JP": "asia", "CN": "asia",
	"AE": "asia", "IL": "asia",
}

func DetectRegion(country string) string {
	return regionMap[strings.ToUpper(country)]
}

func normalizeCompany(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Join(strings.Fields(s), " ")
	lower := strings.ToLower(s)
	for _, suffix := range companySuffixes {
		if strings.HasSuffix(lower, suffix) {
			s = s[:len(s)-len(suffix)]
			s = strings.TrimSpace(s)
			break
		}
	}
	return s
}

func sanitizeDescription(s string) string {
	s = strings.ReplaceAll(s, "\x00", "")
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}

func ExternalToVariant(ext domain.ExternalJob, sourceID int64, country, sourceBoard string, scrapedAt time.Time) domain.JobVariant {
	title := strings.TrimSpace(ext.Title)
	company := normalizeCompany(ext.Company)
	location := strings.TrimSpace(ext.LocationText)
	desc := sanitizeDescription(ext.Description)

	country = strings.ToUpper(strings.TrimSpace(country))
	currency := strings.ToUpper(strings.TrimSpace(ext.Currency))
	remoteType := strings.ToLower(strings.TrimSpace(ext.RemoteType))
	employmentType := strings.ToLower(strings.TrimSpace(ext.EmploymentType))

	raw := ext.ExternalID + "|" + title + "|" + company + "|" + location + "|" + desc
	hashBytes := sha256.Sum256([]byte(raw))
	contentHash := fmt.Sprintf("%x", hashBytes)

	externalID := strings.TrimSpace(ext.ExternalID)
	if externalID == "" {
		externalID = contentHash[:16]
	}

	hardKey := domain.BuildHardKey(company, title, location, externalID)

	return domain.JobVariant{
		ExternalJobID:  externalID,
		SourceID:       sourceID,
		SourceURL:      strings.TrimSpace(ext.SourceURL),
		ApplyURL:       strings.TrimSpace(ext.ApplyURL),
		Title:          title,
		Company:        company,
		LocationText:   location,
		Country:        country,
		Region:         DetectRegion(country),
		Language:       "",
		SourceBoard:    sourceBoard,
		RemoteType:     remoteType,
		EmploymentType: employmentType,
		SalaryMin:      ext.SalaryMin,
		SalaryMax:      ext.SalaryMax,
		Currency:       currency,
		DescriptionText: desc,
		PostedAt:       ext.PostedAt,
		ScrapedAt:      scrapedAt,
		ContentHash:    contentHash,
		HardKey:        hardKey,
	}
}
```

- [ ] **Step 3: Run tests**

Run: `cd /home/j/code/stawi.opportunities && go test ./pkg/normalize/... -v`
Expected: All PASS

- [ ] **Step 4: Commit**

```bash
git add pkg/normalize/
git commit -m "feat: add normalization with company cleanup and region detection"
```

---

## Task 7: Dedupe Engine

**Files:**
- Create: `pkg/dedupe/dedupe.go`
- Create: `pkg/dedupe/dedupe_test.go`

- [ ] **Step 1: Write dedupe engine**

Create `pkg/dedupe/dedupe.go`:

```go
package dedupe

import (
	"context"
	"time"

	"stawi.opportunities/pkg/domain"
	"stawi.opportunities/pkg/repository"
)

type Engine struct {
	jobRepo *repository.JobRepository
}

func NewEngine(jobRepo *repository.JobRepository) *Engine {
	return &Engine{jobRepo: jobRepo}
}

func (e *Engine) UpsertAndCluster(ctx context.Context, variant *domain.JobVariant) (*domain.CanonicalJob, error) {
	if err := e.jobRepo.UpsertVariant(ctx, variant); err != nil {
		return nil, err
	}

	existing, err := e.jobRepo.FindByHardKey(ctx, variant.HardKey)
	if err != nil {
		return nil, err
	}

	confidence := 1.0
	if existing != nil && existing.ID != variant.ID {
		confidence = 0.98
	}

	cluster := &domain.JobCluster{
		CanonicalVariantID: variant.ID,
		Confidence:         confidence,
	}
	if err := e.jobRepo.CreateCluster(ctx, cluster); err != nil {
		return nil, err
	}

	member := &domain.JobClusterMember{
		ClusterID: cluster.ID,
		VariantID: variant.ID,
		MatchType: "hard",
		Score:     confidence,
	}
	if err := e.jobRepo.AddClusterMember(ctx, member); err != nil {
		return nil, err
	}

	now := time.Now()
	canonical := &domain.CanonicalJob{
		ClusterID:       cluster.ID,
		Title:           variant.Title,
		Company:         variant.Company,
		DescriptionText: variant.DescriptionText,
		LocationText:    variant.LocationText,
		Country:         variant.Country,
		RemoteType:      variant.RemoteType,
		EmploymentType:  variant.EmploymentType,
		SalaryMin:       variant.SalaryMin,
		SalaryMax:       variant.SalaryMax,
		Currency:        variant.Currency,
		ApplyURL:        variant.ApplyURL,
		PostedAt:        variant.PostedAt,
		FirstSeenAt:     now,
		LastSeenAt:      now,
		IsActive:        true,
	}
	if err := e.jobRepo.UpsertCanonical(ctx, canonical); err != nil {
		return nil, err
	}

	return canonical, nil
}
```

- [ ] **Step 2: Commit**

```bash
git add pkg/dedupe/
git commit -m "feat: add hard-key dedupe engine with cluster management"
```

---

## Task 8: Seed Files + Loader

**Files:**
- Create: `seeds/apis.json`
- Create: `seeds/africa/ke.json`
- Create: `seeds/africa/ng.json`
- Create: `seeds/africa/za.json`
- Create: `seeds/africa/gh.json`
- Create: `seeds/africa/ug.json`
- Create: `seeds/africa/pan-african.json`
- Create: `seeds/europe/uk.json`
- Create: `seeds/oceania/au.json`
- Create: `seeds/greenhouse_boards.json`
- Create: `seeds/lever_boards.json`
- Create: `seeds/workday_sites.json`
- Create: `pkg/seeds/loader.go`

- [ ] **Step 1: Create seed files**

Create `seeds/apis.json`:

```json
[
  {"source_type": "remoteok", "base_url": "https://remoteok.com", "country": "", "region": "", "crawl_interval_sec": 3600, "priority": "hot"},
  {"source_type": "arbeitnow", "base_url": "https://arbeitnow.com", "country": "", "region": "europe", "crawl_interval_sec": 3600, "priority": "hot"},
  {"source_type": "jobicy", "base_url": "https://jobicy.com", "country": "", "region": "", "crawl_interval_sec": 3600, "priority": "hot"},
  {"source_type": "themuse", "base_url": "https://www.themuse.com", "country": "", "region": "", "crawl_interval_sec": 7200, "priority": "normal"},
  {"source_type": "himalayas", "base_url": "https://himalayas.app", "country": "", "region": "", "crawl_interval_sec": 7200, "priority": "normal"},
  {"source_type": "findwork", "base_url": "https://findwork.dev", "country": "", "region": "", "crawl_interval_sec": 7200, "priority": "normal"}
]
```

Create `seeds/africa/ke.json`:

```json
[
  {"source_type": "brightermonday", "base_url": "https://www.brightermonday.co.ke", "country": "KE", "region": "east_africa", "crawl_interval_sec": 3600, "priority": "hot"}
]
```

Create `seeds/africa/ng.json`:

```json
[
  {"source_type": "jobberman", "base_url": "https://www.jobberman.com", "country": "NG", "region": "west_africa", "crawl_interval_sec": 3600, "priority": "hot"},
  {"source_type": "myjobmag", "base_url": "https://www.myjobmag.com", "country": "NG", "region": "west_africa", "crawl_interval_sec": 3600, "priority": "hot"}
]
```

Create `seeds/africa/za.json`:

```json
[
  {"source_type": "careers24", "base_url": "https://www.careers24.com", "country": "ZA", "region": "southern_africa", "crawl_interval_sec": 3600, "priority": "hot"},
  {"source_type": "pnet", "base_url": "https://www.pnet.co.za", "country": "ZA", "region": "southern_africa", "crawl_interval_sec": 3600, "priority": "hot"}
]
```

Create `seeds/africa/gh.json`:

```json
[
  {"source_type": "jobberman", "base_url": "https://www.jobberman.com.gh", "country": "GH", "region": "west_africa", "crawl_interval_sec": 7200, "priority": "normal"}
]
```

Create `seeds/africa/ug.json`:

```json
[
  {"source_type": "brightermonday", "base_url": "https://www.brightermonday.co.ug", "country": "UG", "region": "east_africa", "crawl_interval_sec": 7200, "priority": "normal"}
]
```

Create `seeds/africa/pan-african.json`:

```json
[
  {"source_type": "njorku", "base_url": "https://www.njorku.com", "country": "", "region": "", "crawl_interval_sec": 3600, "priority": "hot"},
  {"source_type": "myjobmag", "base_url": "https://www.myjobmag.co.ke", "country": "KE", "region": "east_africa", "crawl_interval_sec": 7200, "priority": "normal"},
  {"source_type": "myjobmag", "base_url": "https://www.myjobmag.co.za", "country": "ZA", "region": "southern_africa", "crawl_interval_sec": 7200, "priority": "normal"},
  {"source_type": "myjobmag", "base_url": "https://www.myjobmag.com.gh", "country": "GH", "region": "west_africa", "crawl_interval_sec": 7200, "priority": "normal"}
]
```

Create `seeds/europe/uk.json`:

```json
[
  {"source_type": "greenhouse", "base_url": "https://boards.greenhouse.io/deliveroo", "country": "GB", "region": "europe", "crawl_interval_sec": 14400, "priority": "normal"},
  {"source_type": "lever", "base_url": "https://jobs.lever.co/revolut", "country": "GB", "region": "europe", "crawl_interval_sec": 14400, "priority": "normal"}
]
```

Create `seeds/oceania/au.json`:

```json
[
  {"source_type": "greenhouse", "base_url": "https://boards.greenhouse.io/canva", "country": "AU", "region": "oceania", "crawl_interval_sec": 14400, "priority": "normal"},
  {"source_type": "greenhouse", "base_url": "https://boards.greenhouse.io/atlassian", "country": "AU", "region": "oceania", "crawl_interval_sec": 14400, "priority": "normal"}
]
```

Create `seeds/greenhouse_boards.json`:

```json
[
  {"source_type": "greenhouse", "base_url": "https://boards.greenhouse.io/stripe", "country": "US", "region": "americas", "crawl_interval_sec": 14400, "priority": "normal"},
  {"source_type": "greenhouse", "base_url": "https://boards.greenhouse.io/airbnb", "country": "US", "region": "americas", "crawl_interval_sec": 14400, "priority": "normal"},
  {"source_type": "greenhouse", "base_url": "https://boards.greenhouse.io/doordash", "country": "US", "region": "americas", "crawl_interval_sec": 14400, "priority": "normal"},
  {"source_type": "greenhouse", "base_url": "https://boards.greenhouse.io/cloudflare", "country": "US", "region": "americas", "crawl_interval_sec": 14400, "priority": "normal"},
  {"source_type": "greenhouse", "base_url": "https://boards.greenhouse.io/twitch", "country": "US", "region": "americas", "crawl_interval_sec": 14400, "priority": "normal"},
  {"source_type": "greenhouse", "base_url": "https://boards.greenhouse.io/figma", "country": "US", "region": "americas", "crawl_interval_sec": 14400, "priority": "normal"},
  {"source_type": "greenhouse", "base_url": "https://boards.greenhouse.io/reddit", "country": "US", "region": "americas", "crawl_interval_sec": 14400, "priority": "normal"},
  {"source_type": "greenhouse", "base_url": "https://boards.greenhouse.io/andela", "country": "", "region": "", "crawl_interval_sec": 7200, "priority": "hot"},
  {"source_type": "greenhouse", "base_url": "https://boards.greenhouse.io/shopify", "country": "CA", "region": "americas", "crawl_interval_sec": 14400, "priority": "normal"},
  {"source_type": "greenhouse", "base_url": "https://boards.greenhouse.io/gitlab", "country": "", "region": "", "crawl_interval_sec": 7200, "priority": "hot"}
]
```

Create `seeds/lever_boards.json`:

```json
[
  {"source_type": "lever", "base_url": "https://jobs.lever.co/netflix", "country": "US", "region": "americas", "crawl_interval_sec": 14400, "priority": "normal"},
  {"source_type": "lever", "base_url": "https://jobs.lever.co/twilio", "country": "US", "region": "americas", "crawl_interval_sec": 14400, "priority": "normal"},
  {"source_type": "lever", "base_url": "https://jobs.lever.co/datadog", "country": "US", "region": "americas", "crawl_interval_sec": 14400, "priority": "normal"}
]
```

Create `seeds/workday_sites.json`:

```json
[
  {"source_type": "workday", "base_url": "https://wd3.myworkdaysite.com/recruiting/microsoft", "country": "US", "region": "americas", "crawl_interval_sec": 14400, "priority": "normal"}
]
```

- [ ] **Step 2: Create seed loader**

Create `pkg/seeds/loader.go`:

```go
package seeds

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"

	"stawi.opportunities/pkg/domain"
	"stawi.opportunities/pkg/repository"
)

type SeedEntry struct {
	SourceType       domain.SourceType `json:"source_type"`
	BaseURL          string            `json:"base_url"`
	Country          string            `json:"country"`
	Region           string            `json:"region"`
	CrawlIntervalSec int              `json:"crawl_interval_sec"`
	Priority         domain.Priority   `json:"priority"`
}

func LoadAndUpsert(ctx context.Context, seedsDir string, repo *repository.SourceRepository) (int, error) {
	count := 0
	err := filepath.WalkDir(seedsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if filepath.Ext(path) != ".json" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		var entries []SeedEntry
		if err := json.Unmarshal(data, &entries); err != nil {
			return err
		}

		for _, e := range entries {
			src := &domain.Source{
				SourceType:       e.SourceType,
				BaseURL:          e.BaseURL,
				Country:          e.Country,
				Region:           e.Region,
				CrawlIntervalSec: e.CrawlIntervalSec,
				Priority:         e.Priority,
				Status:           domain.SourceStatusActive,
				HealthScore:      1.0,
			}
			if err := repo.Upsert(ctx, src); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	return count, err
}
```

- [ ] **Step 3: Commit**

```bash
git add seeds/ pkg/seeds/
git commit -m "feat: add seed files for global sources and seed loader"
```

---

## Task 9: Free JSON API Connectors (6 connectors)

**Files:**
- Create: `pkg/connectors/remoteok/remoteok.go`
- Create: `pkg/connectors/arbeitnow/arbeitnow.go`
- Create: `pkg/connectors/jobicy/jobicy.go`
- Create: `pkg/connectors/themuse/themuse.go`
- Create: `pkg/connectors/himalayas/himalayas.go`
- Create: `pkg/connectors/findwork/findwork.go`

Each connector implements the `Connector` interface and returns a `CrawlIterator`. These are all free JSON APIs requiring no API keys.

- [ ] **Step 1: Create RemoteOK connector**

Create `pkg/connectors/remoteok/remoteok.go`:

```go
package remoteok

import (
	"context"
	"encoding/json"

	"stawi.opportunities/pkg/connectors"
	"stawi.opportunities/pkg/connectors/httpx"
	"stawi.opportunities/pkg/domain"
)

type Connector struct {
	client *httpx.Client
}

func New(client *httpx.Client) *Connector {
	return &Connector{client: client}
}

func (c *Connector) Type() domain.SourceType { return domain.SourceTypeRemoteOK }

func (c *Connector) Crawl(ctx context.Context, source domain.Source) connectors.CrawlIterator {
	body, status, err := c.client.Get(ctx, "https://remoteok.com/api", nil)
	if err != nil {
		return connectors.NewSinglePageIterator(nil, nil, 0, err)
	}

	var raw []json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return connectors.NewSinglePageIterator(nil, body, status, err)
	}

	var jobs []domain.ExternalJob
	for _, entry := range raw {
		var item struct {
			ID           int    `json:"id"`
			Slug         string `json:"slug"`
			Company      string `json:"company"`
			Position     string `json:"position"`
			Location     string `json:"location"`
			Description  string `json:"description"`
			URL          string `json:"url"`
			ApplyURL     string `json:"apply_url"`
			Date         string `json:"date"`
			SalaryMin    int    `json:"salary_min"`
			SalaryMax    int    `json:"salary_max"`
		}
		if json.Unmarshal(entry, &item) != nil || item.Position == "" {
			continue
		}
		applyURL := item.ApplyURL
		if applyURL == "" {
			applyURL = item.URL
		}
		jobs = append(jobs, domain.ExternalJob{
			ExternalID:   item.Slug,
			Title:        item.Position,
			Company:      item.Company,
			LocationText: item.Location,
			Description:  item.Description,
			ApplyURL:     applyURL,
			RemoteType:   "remote",
			SalaryMin:    float64(item.SalaryMin),
			SalaryMax:    float64(item.SalaryMax),
			Currency:     "USD",
		})
	}
	return connectors.NewSinglePageIterator(jobs, body, status, nil)
}
```

- [ ] **Step 2: Create Arbeitnow connector**

Create `pkg/connectors/arbeitnow/arbeitnow.go`:

```go
package arbeitnow

import (
	"context"
	"encoding/json"
	"fmt"

	"stawi.opportunities/pkg/connectors"
	"stawi.opportunities/pkg/connectors/httpx"
	"stawi.opportunities/pkg/domain"
)

type Connector struct {
	client *httpx.Client
}

func New(client *httpx.Client) *Connector {
	return &Connector{client: client}
}

func (c *Connector) Type() domain.SourceType { return domain.SourceTypeArbeitnow }

func (c *Connector) Crawl(ctx context.Context, source domain.Source) connectors.CrawlIterator {
	return &iterator{client: c.client, page: 1}
}

type iterator struct {
	client  *httpx.Client
	page    int
	jobs    []domain.ExternalJob
	raw     []byte
	status  int
	err     error
	done    bool
}

func (it *iterator) Next(ctx context.Context) bool {
	if it.done || it.err != nil {
		return false
	}

	url := fmt.Sprintf("https://www.arbeitnow.com/api/job-board-api?page=%d", it.page)
	body, status, err := it.client.Get(ctx, url, nil)
	if err != nil {
		it.err = err
		return false
	}
	it.raw = body
	it.status = status

	var resp struct {
		Data []struct {
			Slug        string   `json:"slug"`
			Title       string   `json:"title"`
			CompanyName string   `json:"company_name"`
			Location    string   `json:"location"`
			Description string   `json:"description"`
			URL         string   `json:"url"`
			Remote      bool     `json:"remote"`
			Tags        []string `json:"tags"`
		} `json:"data"`
		Links struct {
			Next string `json:"next"`
		} `json:"links"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		it.err = err
		return false
	}

	if len(resp.Data) == 0 {
		it.done = true
		return false
	}

	it.jobs = nil
	for _, item := range resp.Data {
		remoteType := ""
		if item.Remote {
			remoteType = "remote"
		}
		it.jobs = append(it.jobs, domain.ExternalJob{
			ExternalID:   item.Slug,
			Title:        item.Title,
			Company:      item.CompanyName,
			LocationText: item.Location,
			Description:  item.Description,
			ApplyURL:     item.URL,
			RemoteType:   remoteType,
		})
	}

	if resp.Links.Next == "" {
		it.done = true
	}
	it.page++
	return true
}

func (it *iterator) Jobs() []domain.ExternalJob    { return it.jobs }
func (it *iterator) RawPayload() []byte             { return it.raw }
func (it *iterator) HTTPStatus() int                { return it.status }
func (it *iterator) Err() error                     { return it.err }
func (it *iterator) Cursor() json.RawMessage        { return nil }
```

- [ ] **Step 3: Create Jobicy connector**

Create `pkg/connectors/jobicy/jobicy.go`:

```go
package jobicy

import (
	"context"
	"encoding/json"
	"fmt"

	"stawi.opportunities/pkg/connectors"
	"stawi.opportunities/pkg/connectors/httpx"
	"stawi.opportunities/pkg/domain"
)

type Connector struct {
	client *httpx.Client
}

func New(client *httpx.Client) *Connector {
	return &Connector{client: client}
}

func (c *Connector) Type() domain.SourceType { return domain.SourceTypeJobicy }

func (c *Connector) Crawl(ctx context.Context, source domain.Source) connectors.CrawlIterator {
	url := "https://jobicy.com/api/v2/remote-jobs?count=50"
	body, status, err := c.client.Get(ctx, url, nil)
	if err != nil {
		return connectors.NewSinglePageIterator(nil, nil, 0, err)
	}

	var resp struct {
		Jobs []struct {
			ID          int    `json:"id"`
			URL         string `json:"url"`
			JobTitle    string `json:"jobTitle"`
			CompanyName string `json:"companyName"`
			JobGeo      string `json:"jobGeo"`
			JobType     string `json:"jobType"`
			JobExcerpt  string `json:"jobExcerpt"`
			AnnSalary   string `json:"annualSalaryMin"`
			AnnSalMax   string `json:"annualSalaryMax"`
			SalCurrency string `json:"salaryCurrency"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return connectors.NewSinglePageIterator(nil, body, status, err)
	}

	var jobs []domain.ExternalJob
	for _, item := range resp.Jobs {
		jobs = append(jobs, domain.ExternalJob{
			ExternalID:     fmt.Sprintf("%d", item.ID),
			Title:          item.JobTitle,
			Company:        item.CompanyName,
			LocationText:   item.JobGeo,
			Description:    item.JobExcerpt,
			ApplyURL:       item.URL,
			RemoteType:     "remote",
			EmploymentType: item.JobType,
			Currency:       item.SalCurrency,
		})
	}
	return connectors.NewSinglePageIterator(jobs, body, status, nil)
}
```

- [ ] **Step 4: Create TheMuse connector**

Create `pkg/connectors/themuse/themuse.go`:

```go
package themuse

import (
	"context"
	"encoding/json"
	"fmt"

	"stawi.opportunities/pkg/connectors"
	"stawi.opportunities/pkg/connectors/httpx"
	"stawi.opportunities/pkg/domain"
)

type Connector struct {
	client *httpx.Client
}

func New(client *httpx.Client) *Connector {
	return &Connector{client: client}
}

func (c *Connector) Type() domain.SourceType { return domain.SourceTypeTheMuse }

func (c *Connector) Crawl(ctx context.Context, source domain.Source) connectors.CrawlIterator {
	return &iterator{client: c.client, page: 0}
}

type iterator struct {
	client *httpx.Client
	page   int
	jobs   []domain.ExternalJob
	raw    []byte
	status int
	err    error
	done   bool
}

func (it *iterator) Next(ctx context.Context) bool {
	if it.done || it.err != nil || it.page > 50 {
		return false
	}

	url := fmt.Sprintf("https://www.themuse.com/api/public/jobs?page=%d", it.page)
	body, status, err := it.client.Get(ctx, url, nil)
	if err != nil {
		it.err = err
		return false
	}
	it.raw = body
	it.status = status

	var resp struct {
		Results []struct {
			ID       int    `json:"id"`
			Name     string `json:"name"`
			Company  struct {
				Name string `json:"name"`
			} `json:"company"`
			Locations []struct {
				Name string `json:"name"`
			} `json:"locations"`
			Contents string `json:"contents"`
			Refs     struct {
				LandingPage string `json:"landing_page"`
			} `json:"refs"`
			Type string `json:"type"`
		} `json:"results"`
		PageCount int `json:"page_count"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		it.err = err
		return false
	}

	if len(resp.Results) == 0 {
		it.done = true
		return false
	}

	it.jobs = nil
	for _, item := range resp.Results {
		location := ""
		if len(item.Locations) > 0 {
			location = item.Locations[0].Name
		}
		it.jobs = append(it.jobs, domain.ExternalJob{
			ExternalID:     fmt.Sprintf("%d", item.ID),
			Title:          item.Name,
			Company:        item.Company.Name,
			LocationText:   location,
			Description:    item.Contents,
			ApplyURL:       item.Refs.LandingPage,
			EmploymentType: item.Type,
		})
	}

	it.page++
	if it.page >= resp.PageCount {
		it.done = true
	}
	return true
}

func (it *iterator) Jobs() []domain.ExternalJob    { return it.jobs }
func (it *iterator) RawPayload() []byte             { return it.raw }
func (it *iterator) HTTPStatus() int                { return it.status }
func (it *iterator) Err() error                     { return it.err }
func (it *iterator) Cursor() json.RawMessage        { return nil }
```

- [ ] **Step 5: Create Himalayas connector**

Create `pkg/connectors/himalayas/himalayas.go`:

```go
package himalayas

import (
	"context"
	"encoding/json"
	"fmt"

	"stawi.opportunities/pkg/connectors"
	"stawi.opportunities/pkg/connectors/httpx"
	"stawi.opportunities/pkg/domain"
)

type Connector struct {
	client *httpx.Client
}

func New(client *httpx.Client) *Connector {
	return &Connector{client: client}
}

func (c *Connector) Type() domain.SourceType { return domain.SourceTypeHimalayas }

func (c *Connector) Crawl(ctx context.Context, source domain.Source) connectors.CrawlIterator {
	return &iterator{client: c.client, page: 1}
}

type iterator struct {
	client *httpx.Client
	page   int
	jobs   []domain.ExternalJob
	raw    []byte
	status int
	err    error
	done   bool
}

func (it *iterator) Next(ctx context.Context) bool {
	if it.done || it.err != nil {
		return false
	}

	url := fmt.Sprintf("https://himalayas.app/jobs/api?page=%d&limit=50", it.page)
	body, status, err := it.client.Get(ctx, url, nil)
	if err != nil {
		it.err = err
		return false
	}
	it.raw = body
	it.status = status

	var resp struct {
		Jobs []struct {
			ID               string `json:"id"`
			Title            string `json:"title"`
			CompanyName      string `json:"companyName"`
			Location         string `json:"location"`
			Description      string `json:"description"`
			ApplicationLink  string `json:"applicationLink"`
			ExternalURL      string `json:"externalUrl"`
			SalaryCurrency   string `json:"salaryCurrency"`
			SalaryMin        int    `json:"salaryMin"`
			SalaryMax        int    `json:"salaryMax"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		it.err = err
		return false
	}

	if len(resp.Jobs) == 0 {
		it.done = true
		return false
	}

	it.jobs = nil
	for _, item := range resp.Jobs {
		applyURL := item.ApplicationLink
		if applyURL == "" {
			applyURL = item.ExternalURL
		}
		it.jobs = append(it.jobs, domain.ExternalJob{
			ExternalID:   item.ID,
			Title:        item.Title,
			Company:      item.CompanyName,
			LocationText: item.Location,
			Description:  item.Description,
			ApplyURL:     applyURL,
			RemoteType:   "remote",
			SalaryMin:    float64(item.SalaryMin),
			SalaryMax:    float64(item.SalaryMax),
			Currency:     item.SalaryCurrency,
		})
	}

	it.page++
	return true
}

func (it *iterator) Jobs() []domain.ExternalJob    { return it.jobs }
func (it *iterator) RawPayload() []byte             { return it.raw }
func (it *iterator) HTTPStatus() int                { return it.status }
func (it *iterator) Err() error                     { return it.err }
func (it *iterator) Cursor() json.RawMessage        { return nil }
```

- [ ] **Step 6: Create FindWork connector**

Create `pkg/connectors/findwork/findwork.go`:

```go
package findwork

import (
	"context"
	"encoding/json"
	"fmt"

	"stawi.opportunities/pkg/connectors"
	"stawi.opportunities/pkg/connectors/httpx"
	"stawi.opportunities/pkg/domain"
)

type Connector struct {
	client *httpx.Client
}

func New(client *httpx.Client) *Connector {
	return &Connector{client: client}
}

func (c *Connector) Type() domain.SourceType { return domain.SourceTypeFindWork }

func (c *Connector) Crawl(ctx context.Context, source domain.Source) connectors.CrawlIterator {
	return &iterator{client: c.client, page: 1}
}

type iterator struct {
	client *httpx.Client
	page   int
	jobs   []domain.ExternalJob
	raw    []byte
	status int
	err    error
	done   bool
}

func (it *iterator) Next(ctx context.Context) bool {
	if it.done || it.err != nil {
		return false
	}

	url := fmt.Sprintf("https://findwork.dev/api/jobs/?page=%d", it.page)
	body, status, err := it.client.Get(ctx, url, nil)
	if err != nil {
		it.err = err
		return false
	}
	it.raw = body
	it.status = status

	var resp struct {
		Results []struct {
			ID          int    `json:"id"`
			Role        string `json:"role"`
			CompanyName string `json:"company_name"`
			Location    string `json:"location"`
			Text        string `json:"text"`
			URL         string `json:"url"`
			Remote      bool   `json:"remote"`
			Keywords    []string `json:"keywords"`
		} `json:"results"`
		Next string `json:"next"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		it.err = err
		return false
	}

	if len(resp.Results) == 0 {
		it.done = true
		return false
	}

	it.jobs = nil
	for _, item := range resp.Results {
		remoteType := ""
		if item.Remote {
			remoteType = "remote"
		}
		it.jobs = append(it.jobs, domain.ExternalJob{
			ExternalID:   fmt.Sprintf("%d", item.ID),
			Title:        item.Role,
			Company:      item.CompanyName,
			LocationText: item.Location,
			Description:  item.Text,
			ApplyURL:     item.URL,
			RemoteType:   remoteType,
		})
	}

	if resp.Next == "" {
		it.done = true
	}
	it.page++
	return true
}

func (it *iterator) Jobs() []domain.ExternalJob    { return it.jobs }
func (it *iterator) RawPayload() []byte             { return it.raw }
func (it *iterator) HTTPStatus() int                { return it.status }
func (it *iterator) Err() error                     { return it.err }
func (it *iterator) Cursor() json.RawMessage        { return nil }
```

- [ ] **Step 7: Verify all connectors compile**

Run: `cd /home/j/code/stawi.opportunities && go build ./pkg/connectors/...`

- [ ] **Step 8: Commit**

```bash
git add pkg/connectors/remoteok/ pkg/connectors/arbeitnow/ pkg/connectors/jobicy/ pkg/connectors/themuse/ pkg/connectors/himalayas/ pkg/connectors/findwork/
git commit -m "feat: add 6 free JSON API connectors (remoteok, arbeitnow, jobicy, themuse, himalayas, findwork)"
```

---

## Task 10: African Board Connectors (6 connectors)

**Files:**
- Create: `pkg/connectors/brightermonday/brightermonday.go`
- Create: `pkg/connectors/jobberman/jobberman.go`
- Create: `pkg/connectors/myjobmag/myjobmag.go`
- Create: `pkg/connectors/njorku/njorku.go`
- Create: `pkg/connectors/careers24/careers24.go`
- Create: `pkg/connectors/pnet/pnet.go`

These are HTML-parsing connectors. Each must crawl listing pages to discover job URLs, then fetch detail pages to extract the 5 required fields. The exact HTML structure for each site will need to be verified at runtime and adjusted — build the skeleton with best-effort CSS/regex selectors based on known site structures, and iterate.

**Note to implementer:** African job board HTML structures change frequently. Build these connectors defensively — use fallback selectors, log parsing failures, and let the quality gate reject incomplete jobs rather than crashing. The first implementation will likely need tuning after seeing real responses.

- [ ] **Step 1: Create BrighterMonday connector**

Create `pkg/connectors/brightermonday/brightermonday.go`:

```go
package brightermonday

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"stawi.opportunities/pkg/connectors"
	"stawi.opportunities/pkg/connectors/httpx"
	"stawi.opportunities/pkg/domain"
)

type Connector struct {
	client *httpx.Client
}

func New(client *httpx.Client) *Connector {
	return &Connector{client: client}
}

func (c *Connector) Type() domain.SourceType { return domain.SourceTypeBrighterMonday }

func (c *Connector) Crawl(ctx context.Context, source domain.Source) connectors.CrawlIterator {
	return &iterator{client: c.client, baseURL: source.BaseURL, page: 1}
}

var (
	jobLinkRe    = regexp.MustCompile(`href=["']((?:/|https?://[^"']*brightermonday[^"']*/)[^"']*jobs?/[^"']+)["']`)
	titleRe      = regexp.MustCompile(`(?i)<h1[^>]*class="[^"]*listing-title[^"]*"[^>]*>([^<]+)</h1>`)
	companyRe    = regexp.MustCompile(`(?i)<a[^>]*class="[^"]*company-link[^"]*"[^>]*>([^<]+)</a>`)
	locationRe   = regexp.MustCompile(`(?i)<span[^>]*class="[^"]*location[^"]*"[^>]*>([^<]+)</span>`)
	descriptionRe = regexp.MustCompile(`(?is)<div[^>]*class="[^"]*job-description[^"]*"[^>]*>(.*?)</div>`)
)

type iterator struct {
	client  *httpx.Client
	baseURL string
	page    int
	jobs    []domain.ExternalJob
	raw     []byte
	status  int
	err     error
	done    bool
}

func (it *iterator) Next(ctx context.Context) bool {
	if it.done || it.err != nil || it.page > 100 {
		return false
	}

	listURL := fmt.Sprintf("%s/jobs?page=%d", it.baseURL, it.page)
	body, status, err := it.client.Get(ctx, listURL, nil)
	if err != nil {
		it.err = err
		return false
	}
	it.raw = body
	it.status = status

	html := string(body)
	matches := jobLinkRe.FindAllStringSubmatch(html, -1)
	if len(matches) == 0 {
		it.done = true
		return false
	}

	seen := make(map[string]bool)
	it.jobs = nil
	for _, m := range matches {
		link := m[1]
		if !strings.HasPrefix(link, "http") {
			link = it.baseURL + link
		}
		if seen[link] {
			continue
		}
		seen[link] = true

		job := it.fetchDetail(ctx, link)
		if job != nil {
			it.jobs = append(it.jobs, *job)
		}
	}

	it.page++
	if len(it.jobs) == 0 {
		it.done = true
		return false
	}
	return true
}

func (it *iterator) fetchDetail(ctx context.Context, url string) *domain.ExternalJob {
	body, _, err := it.client.Get(ctx, url, nil)
	if err != nil {
		return nil
	}
	html := string(body)

	title := extractFirst(titleRe, html)
	company := extractFirst(companyRe, html)
	location := extractFirst(locationRe, html)
	description := stripTags(extractFirst(descriptionRe, html))

	if title == "" {
		title = extractTitle(html)
	}

	return &domain.ExternalJob{
		ExternalID:   url,
		Title:        title,
		Company:      company,
		LocationText: location,
		Description:  description,
		ApplyURL:     url,
		SourceURL:    url,
	}
}

func extractFirst(re *regexp.Regexp, html string) string {
	m := re.FindStringSubmatch(html)
	if len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func extractTitle(html string) string {
	re := regexp.MustCompile(`(?i)<title>([^<]+)</title>`)
	m := re.FindStringSubmatch(html)
	if len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

var tagRe = regexp.MustCompile(`<[^>]+>`)

func stripTags(s string) string {
	return strings.TrimSpace(tagRe.ReplaceAllString(s, " "))
}

func (it *iterator) Jobs() []domain.ExternalJob    { return it.jobs }
func (it *iterator) RawPayload() []byte             { return it.raw }
func (it *iterator) HTTPStatus() int                { return it.status }
func (it *iterator) Err() error                     { return it.err }
func (it *iterator) Cursor() json.RawMessage        { return nil }
```

- [ ] **Step 2: Create Jobberman, MyJobMag, Njorku, Careers24, PNet connectors**

These follow the same pattern as BrighterMonday — HTML listing pages with detail page fetching. Each has site-specific regex selectors. Create them using the same skeleton structure as BrighterMonday but with adjusted URL patterns and selectors:

**Jobberman** (`pkg/connectors/jobberman/jobberman.go`): List URL `{baseURL}/jobs?page={n}`, job links matching `/jobs/`, detail page parsing.

**MyJobMag** (`pkg/connectors/myjobmag/myjobmag.go`): List URL `{baseURL}/jobs?page={n}`, similar pattern.

**Njorku** (`pkg/connectors/njorku/njorku.go`): List URL `{baseURL}/search?page={n}`, aggregator-style.

**Careers24** (`pkg/connectors/careers24/careers24.go`): List URL `{baseURL}/jobs?page={n}`.

**PNet** (`pkg/connectors/pnet/pnet.go`): List URL `{baseURL}/jobs?page={n}`.

Each connector must:
1. Follow the same `Connector` + `CrawlIterator` interface
2. Use `httpx.Client` for HTTP requests
3. Parse job links from listing pages via regex
4. Fetch each detail page for full job data
5. Return `ExternalJob` structs — the quality gate handles validation

**Implementation note:** Copy the BrighterMonday connector structure exactly, changing only `Type()`, `baseURL` patterns, and regex selectors. The regex selectors will need to be tuned against live HTML after deployment — start with generic patterns (`<h1>` for title, `<title>` fallback) and refine.

- [ ] **Step 3: Verify compilation**

Run: `cd /home/j/code/stawi.opportunities && go build ./pkg/connectors/...`

- [ ] **Step 4: Commit**

```bash
git add pkg/connectors/brightermonday/ pkg/connectors/jobberman/ pkg/connectors/myjobmag/ pkg/connectors/njorku/ pkg/connectors/careers24/ pkg/connectors/pnet/
git commit -m "feat: add 6 African board connectors (brightermonday, jobberman, myjobmag, njorku, careers24, pnet)"
```

---

## Task 11: Refactor Existing Connectors to Iterator Interface

**Files:**
- Create: `pkg/connectors/greenhouse/greenhouse.go`
- Create: `pkg/connectors/lever/lever.go`
- Create: `pkg/connectors/workday/workday.go`
- Create: `pkg/connectors/smartrecruiters/smartrecruiters.go`
- Create: `pkg/connectors/schemaorg/schemaorg.go`

Port the existing `internal/connectors/` implementations to the new iterator interface in `pkg/connectors/`. Keep the same parsing logic, wrap in `SinglePageIterator`.

- [ ] **Step 1: Port Greenhouse, Lever, Workday, SmartRecruiters, SchemaOrg**

For each, the pattern is: copy the existing parsing logic from `internal/connectors/{name}/{name}.go`, change the return type to use `connectors.NewSinglePageIterator()`, and update the package path.

Example for Greenhouse (`pkg/connectors/greenhouse/greenhouse.go`):

```go
package greenhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"stawi.opportunities/pkg/connectors"
	"stawi.opportunities/pkg/connectors/httpx"
	"stawi.opportunities/pkg/domain"
)

type Connector struct {
	client *httpx.Client
}

func New(client *httpx.Client) *Connector {
	return &Connector{client: client}
}

func (c *Connector) Type() domain.SourceType { return domain.SourceTypeGreenhouse }

func (c *Connector) Crawl(ctx context.Context, source domain.Source) connectors.CrawlIterator {
	parts := strings.Split(strings.TrimRight(source.BaseURL, "/"), "/")
	company := parts[len(parts)-1]

	url := fmt.Sprintf("https://boards-api.greenhouse.io/v1/boards/%s/jobs?content=true", company)
	body, status, err := c.client.Get(ctx, url, nil)
	if err != nil {
		return connectors.NewSinglePageIterator(nil, nil, 0, err)
	}

	var resp struct {
		Jobs []struct {
			ID       int    `json:"id"`
			Title    string `json:"title"`
			AbsURL   string `json:"absolute_url"`
			Location struct {
				Name string `json:"name"`
			} `json:"location"`
			Content string `json:"content"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return connectors.NewSinglePageIterator(nil, body, status, err)
	}

	var jobs []domain.ExternalJob
	for _, j := range resp.Jobs {
		jobs = append(jobs, domain.ExternalJob{
			ExternalID:   fmt.Sprintf("%d", j.ID),
			Title:        j.Title,
			Company:      company,
			LocationText: j.Location.Name,
			Description:  j.Content,
			ApplyURL:     j.AbsURL,
			SourceURL:    j.AbsURL,
		})
	}
	return connectors.NewSinglePageIterator(jobs, body, status, nil)
}
```

Apply the same pattern for Lever, Workday, SmartRecruiters, and SchemaOrg — porting logic from `internal/connectors/` to `pkg/connectors/` with the iterator interface.

- [ ] **Step 2: Verify compilation and commit**

```bash
cd /home/j/code/stawi.opportunities && go build ./pkg/connectors/...
git add pkg/connectors/greenhouse/ pkg/connectors/lever/ pkg/connectors/workday/ pkg/connectors/smartrecruiters/ pkg/connectors/schemaorg/
git commit -m "feat: port existing connectors to iterator interface"
```

---

## Task 12: Worker Pipeline

**Files:**
- Create: `pkg/pipeline/worker.go`
- Create: `pkg/pipeline/batch.go`

- [ ] **Step 1: Create batch buffer**

Create `pkg/pipeline/batch.go`:

```go
package pipeline

import (
	"context"
	"sync"
	"time"

	"stawi.opportunities/pkg/domain"
	"stawi.opportunities/pkg/repository"
)

type BatchBuffer struct {
	mu        sync.Mutex
	variants  []domain.JobVariant
	repo      *repository.JobRepository
	maxSize   int
	flushTick *time.Ticker
	done      chan struct{}
}

func NewBatchBuffer(repo *repository.JobRepository, maxSize int, flushInterval time.Duration) *BatchBuffer {
	b := &BatchBuffer{
		repo:    repo,
		maxSize: maxSize,
		done:    make(chan struct{}),
	}
	b.flushTick = time.NewTicker(flushInterval)
	go b.flushLoop()
	return b
}

func (b *BatchBuffer) Add(ctx context.Context, v domain.JobVariant) error {
	b.mu.Lock()
	b.variants = append(b.variants, v)
	shouldFlush := len(b.variants) >= b.maxSize
	b.mu.Unlock()

	if shouldFlush {
		return b.Flush(ctx)
	}
	return nil
}

func (b *BatchBuffer) Flush(ctx context.Context) error {
	b.mu.Lock()
	if len(b.variants) == 0 {
		b.mu.Unlock()
		return nil
	}
	batch := b.variants
	b.variants = nil
	b.mu.Unlock()

	return b.repo.UpsertVariants(ctx, batch)
}

func (b *BatchBuffer) flushLoop() {
	for {
		select {
		case <-b.flushTick.C:
			b.Flush(context.Background())
		case <-b.done:
			return
		}
	}
}

func (b *BatchBuffer) Close(ctx context.Context) error {
	b.flushTick.Stop()
	close(b.done)
	return b.Flush(ctx)
}
```

- [ ] **Step 2: Create worker pipeline**

Create `pkg/pipeline/worker.go`:

```go
package pipeline

import (
	"context"
	"encoding/json"
	"time"

	"stawi.opportunities/pkg/connectors"
	"stawi.opportunities/pkg/dedupe"
	"stawi.opportunities/pkg/domain"
	"stawi.opportunities/pkg/normalize"
	"stawi.opportunities/pkg/quality"
	"stawi.opportunities/pkg/repository"
)

type Worker struct {
	registry    *connectors.Registry
	sourceRepo  *repository.SourceRepository
	crawlRepo   *repository.CrawlRepository
	jobRepo     *repository.JobRepository
	rejectRepo  *repository.RejectedRepository
	dedupeEng   *dedupe.Engine
	batch       *BatchBuffer
}

func NewWorker(
	registry *connectors.Registry,
	sourceRepo *repository.SourceRepository,
	crawlRepo *repository.CrawlRepository,
	jobRepo *repository.JobRepository,
	rejectRepo *repository.RejectedRepository,
	dedupeEng *dedupe.Engine,
	batch *BatchBuffer,
) *Worker {
	return &Worker{
		registry:   registry,
		sourceRepo: sourceRepo,
		crawlRepo:  crawlRepo,
		jobRepo:    jobRepo,
		rejectRepo: rejectRepo,
		dedupeEng:  dedupeEng,
		batch:      batch,
	}
}

type CrawlResult struct {
	SourceID      int64
	SourceType    domain.SourceType
	JobsFetched   int
	JobsAccepted  int
	JobsRejected  int
	PagesCrawled  int
	PagesSkipped  int
	Duration      time.Duration
	Error         error
}

func (w *Worker) ProcessRequest(ctx context.Context, req domain.CrawlRequest) CrawlResult {
	start := time.Now()
	result := CrawlResult{
		SourceID:   req.SourceID,
		SourceType: req.SourceType,
	}

	source, err := w.sourceRepo.GetByID(ctx, req.SourceID)
	if err != nil {
		result.Error = err
		return result
	}

	conn, ok := w.registry.Get(source.SourceType)
	if !ok {
		result.Error = err
		return result
	}

	iter := conn.Crawl(ctx, *source)
	for iter.Next(ctx) {
		result.PagesCrawled++

		// Store raw payload for audit
		w.crawlRepo.SaveRawPayload(ctx, &domain.RawPayload{
			CrawlJobID:  0,
			ContentHash: "",
			FetchedAt:   time.Now(),
			HTTPStatus:  iter.HTTPStatus(),
			Body:        iter.RawPayload(),
		})

		for _, job := range iter.Jobs() {
			result.JobsFetched++

			if err := quality.Check(job); err != nil {
				result.JobsRejected++
				rawData, _ := json.Marshal(job)
				w.rejectRepo.Create(ctx, &domain.RejectedJob{
					SourceID:      source.ID,
					SourceType:    source.SourceType,
					ExternalJobID: job.ExternalID,
					Reason:        err.Error(),
					RawData:       rawData,
				})
				continue
			}

			variant := normalize.ExternalToVariant(
				job, source.ID, source.Country,
				string(source.SourceType), time.Now(),
			)

			if err := w.batch.Add(ctx, variant); err != nil {
				continue
			}
			result.JobsAccepted++
		}
	}

	if iter.Err() != nil {
		result.Error = iter.Err()
		newScore := source.HealthScore - 0.2
		if newScore < 0 {
			newScore = 0
		}
		w.sourceRepo.UpdateNextCrawl(ctx, source.ID,
			time.Now().Add(time.Duration(source.CrawlIntervalSec)*time.Second), newScore)
	} else {
		newScore := source.HealthScore + 0.1
		if newScore > 1.0 {
			newScore = 1.0
		}
		w.sourceRepo.UpdateNextCrawl(ctx, source.ID,
			time.Now().Add(time.Duration(source.CrawlIntervalSec)*time.Second), newScore)

		if cursor := iter.Cursor(); cursor != nil {
			w.sourceRepo.UpdateCrawlCursor(ctx, source.ID, cursor)
		}
	}

	result.Duration = time.Since(start)
	return result
}
```

- [ ] **Step 3: Verify compilation**

Run: `cd /home/j/code/stawi.opportunities && go build ./pkg/pipeline/...`

- [ ] **Step 4: Commit**

```bash
git add pkg/pipeline/
git commit -m "feat: add worker pipeline with batch buffer and quality gate integration"
```

---

## Task 13: App Configs + Frame Service Setup

**Files:**
- Create: `apps/crawler/config/config.go`
- Create: `apps/scheduler/config/config.go`
- Create: `apps/api/config/config.go`
- Create: `apps/crawler/service/setup.go`
- Create: `apps/scheduler/service/setup.go`
- Create: `apps/api/service/setup.go`

- [ ] **Step 1: Create crawler config**

Create `apps/crawler/config/config.go`:

```go
package config

import (
	fconfig "github.com/pitabwire/frame/config"
)

type CrawlerConfig struct {
	fconfig.ConfigurationDefault

	WorkerConcurrency int    `env:"WORKER_CONCURRENCY" envDefault:"4"`
	BatchSize         int    `env:"BATCH_SIZE" envDefault:"500"`
	BatchFlushSec     int    `env:"BATCH_FLUSH_SEC" envDefault:"10"`
	SeedsDir          string `env:"SEEDS_DIR" envDefault:"/seeds"`
	UserAgent         string `env:"USER_AGENT" envDefault:"stawi.opportunities-bot/2.0 (+https://stawi.opportunities)"`
	HTTPTimeoutSec    int    `env:"HTTP_TIMEOUT_SEC" envDefault:"20"`

	CrawlQueueName string `env:"CRAWL_QUEUE_NAME" envDefault:"svc.opportunities.crawl"`
	CrawlQueueURI  string `env:"CRAWL_QUEUE_URI" envDefault:"mem://crawl"`
}
```

- [ ] **Step 2: Create scheduler config**

Create `apps/scheduler/config/config.go`:

```go
package config

import (
	fconfig "github.com/pitabwire/frame/config"
)

type SchedulerConfig struct {
	fconfig.ConfigurationDefault

	ScheduleBatchSize  int    `env:"SCHEDULE_BATCH_SIZE" envDefault:"500"`
	ScheduleIntervalSec int   `env:"SCHEDULE_INTERVAL_SEC" envDefault:"30"`
	DiscoveryIntervalSec int  `env:"DISCOVERY_INTERVAL_SEC" envDefault:"600"`
	SeedsDir           string `env:"SEEDS_DIR" envDefault:"/seeds"`

	CrawlQueueName string `env:"CRAWL_QUEUE_NAME" envDefault:"svc.opportunities.crawl"`
	CrawlQueueURI  string `env:"CRAWL_QUEUE_URI" envDefault:"mem://crawl"`
}
```

- [ ] **Step 3: Create api config**

Create `apps/api/config/config.go`:

```go
package config

import (
	fconfig "github.com/pitabwire/frame/config"
)

type APIConfig struct {
	fconfig.ConfigurationDefault
}
```

- [ ] **Step 4: Create crawler service setup**

Create `apps/crawler/service/setup.go`:

```go
package service

import (
	"context"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/datastore"
	"github.com/pitabwire/util"

	aconfig "stawi.opportunities/apps/crawler/config"
	"stawi.opportunities/pkg/connectors"
	"stawi.opportunities/pkg/connectors/arbeitnow"
	"stawi.opportunities/pkg/connectors/brightermonday"
	"stawi.opportunities/pkg/connectors/careers24"
	"stawi.opportunities/pkg/connectors/findwork"
	"stawi.opportunities/pkg/connectors/greenhouse"
	"stawi.opportunities/pkg/connectors/himalayas"
	"stawi.opportunities/pkg/connectors/httpx"
	"stawi.opportunities/pkg/connectors/jobicy"
	"stawi.opportunities/pkg/connectors/jobberman"
	"stawi.opportunities/pkg/connectors/lever"
	"stawi.opportunities/pkg/connectors/myjobmag"
	"stawi.opportunities/pkg/connectors/njorku"
	"stawi.opportunities/pkg/connectors/pnet"
	"stawi.opportunities/pkg/connectors/remoteok"
	"stawi.opportunities/pkg/connectors/schemaorg"
	"stawi.opportunities/pkg/connectors/smartrecruiters"
	"stawi.opportunities/pkg/connectors/themuse"
	"stawi.opportunities/pkg/connectors/workday"
	"stawi.opportunities/pkg/dedupe"
	"stawi.opportunities/pkg/domain"
	"stawi.opportunities/pkg/pipeline"
	"stawi.opportunities/pkg/repository"
)

func BuildRegistry(client *httpx.Client) *connectors.Registry {
	reg := connectors.NewRegistry()
	reg.Register(remoteok.New(client))
	reg.Register(arbeitnow.New(client))
	reg.Register(jobicy.New(client))
	reg.Register(themuse.New(client))
	reg.Register(himalayas.New(client))
	reg.Register(findwork.New(client))
	reg.Register(brightermonday.New(client))
	reg.Register(jobberman.New(client))
	reg.Register(myjobmag.New(client))
	reg.Register(njorku.New(client))
	reg.Register(careers24.New(client))
	reg.Register(pnet.New(client))
	reg.Register(greenhouse.New(client))
	reg.Register(lever.New(client))
	reg.Register(workday.New(client))
	reg.Register(smartrecruiters.New(client))
	reg.Register(schemaorg.New(client))
	return reg
}

type CrawlHandler struct {
	worker *pipeline.Worker
}

func NewCrawlHandler(ctx context.Context, svc *frame.Service, cfg *aconfig.CrawlerConfig) *CrawlHandler {
	pool := svc.DatastoreManager().GetPool(ctx, datastore.DefaultPoolName)
	dbFunc := func(ctx context.Context, readOnly bool) *gorm.DB {
		return pool.DB(ctx, readOnly)
	}

	sourceRepo := repository.NewSourceRepository(dbFunc)
	crawlRepo := repository.NewCrawlRepository(dbFunc)
	jobRepo := repository.NewJobRepository(dbFunc)
	rejectRepo := repository.NewRejectedRepository(dbFunc)

	dedupeEng := dedupe.NewEngine(jobRepo)

	batch := pipeline.NewBatchBuffer(
		jobRepo,
		cfg.BatchSize,
		time.Duration(cfg.BatchFlushSec)*time.Second,
	)

	httpClient := httpx.NewClient(
		time.Duration(cfg.HTTPTimeoutSec)*time.Second,
		cfg.UserAgent,
	)
	registry := BuildRegistry(httpClient)

	worker := pipeline.NewWorker(
		registry, sourceRepo, crawlRepo, jobRepo, rejectRepo, dedupeEng, batch,
	)

	return &CrawlHandler{worker: worker}
}

func (h *CrawlHandler) Name() string { return "crawl.worker" }

func (h *CrawlHandler) PayloadType() any { return &domain.CrawlRequest{} }

func (h *CrawlHandler) Validate(_ context.Context, payload any) error { return nil }

func (h *CrawlHandler) Execute(ctx context.Context, payload any) error {
	req, ok := payload.(*domain.CrawlRequest)
	if !ok {
		return nil
	}

	result := h.worker.ProcessRequest(ctx, *req)

	log := util.Log(ctx)
	log.Info("crawl_batch_complete",
		"source_id", result.SourceID,
		"source_type", result.SourceType,
		"jobs_fetched", result.JobsFetched,
		"jobs_accepted", result.JobsAccepted,
		"jobs_rejected", result.JobsRejected,
		"pages_crawled", result.PagesCrawled,
		"duration_ms", result.Duration.Milliseconds(),
	)

	if result.Error != nil {
		log.Warn("crawl_error",
			"source_id", result.SourceID,
			"error", result.Error.Error(),
		)
	}

	return result.Error
}
```

**Note:** The `gorm.DB` import will need `"gorm.io/gorm"` added. The exact Frame datastore API (`pool.DB(ctx, readOnly)`) should be verified against the Frame source — adjust if the actual method signature differs.

- [ ] **Step 5: Create crawler main.go**

Update `apps/crawler/cmd/main.go`:

```go
package main

import (
	"context"
	"os"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/config"
	"github.com/pitabwire/util"

	aconfig "stawi.opportunities/apps/crawler/config"
	"stawi.opportunities/apps/crawler/service"
	"stawi.opportunities/pkg/domain"
	"stawi.opportunities/pkg/seeds"
	"stawi.opportunities/pkg/repository"
)

func main() {
	ctx := context.Background()

	cfg, err := config.FromEnv[aconfig.CrawlerConfig]()
	if err != nil {
		util.Log(ctx).WithError(err).Fatal("failed to load config")
	}

	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithConfig(&cfg),
		frame.WithDatastore(),
	)
	defer svc.Stop(ctx)

	// Run migrations
	pool := svc.DatastoreManager().GetPool(ctx, "")
	if pool != nil {
		pool.Migrate(ctx, "./migrations")
	}

	// Seed sources
	dbFunc := func(ctx context.Context, readOnly bool) *gorm.DB {
		return pool.DB(ctx, readOnly)
	}
	sourceRepo := repository.NewSourceRepository(dbFunc)
	seedsDir := cfg.SeedsDir
	if _, err := os.Stat(seedsDir); os.IsNotExist(err) {
		seedsDir = "../../seeds"
	}
	count, err := seeds.LoadAndUpsert(ctx, seedsDir, sourceRepo)
	if err != nil {
		util.Log(ctx).Warn("seed loading failed", "error", err)
	} else {
		util.Log(ctx).Info("seeds loaded", "count", count)
	}

	// Setup crawl handler
	handler := service.NewCrawlHandler(ctx, svc, &cfg)

	svc.Init(ctx,
		frame.WithRegisterSubscriber(cfg.CrawlQueueName, cfg.CrawlQueueURI, handler),
	)

	if err := svc.Run(ctx, ""); err != nil {
		util.Log(ctx).WithError(err).Fatal("service stopped")
	}
}
```

- [ ] **Step 6: Create scheduler main.go and service setup**

Create `apps/scheduler/service/setup.go` and `apps/scheduler/cmd/main.go` with the scheduling logic — query due sources, publish CrawlRequests to NATS. Uses a background ticker.

- [ ] **Step 7: Create api main.go and service setup**

Create `apps/api/service/setup.go` and `apps/api/cmd/main.go` with chi HTTP router for `/search`, `/sources`, `/healthz` endpoints.

- [ ] **Step 8: Verify compilation**

Run: `cd /home/j/code/stawi.opportunities && go mod tidy && go build ./...`

- [ ] **Step 9: Commit**

```bash
git add apps/
git commit -m "feat: add Frame-based service configs and main.go for crawler, scheduler, and api"
```

---

## Task 14: Update go.mod Dependencies

**Files:**
- Modify: `go.mod`

- [ ] **Step 1: Add Frame and GORM dependencies**

```bash
cd /home/j/code/stawi.opportunities
go get github.com/pitabwire/frame@latest
go get github.com/pitabwire/util@latest
go get github.com/pitabwire/natspubsub@latest
go get gorm.io/gorm@latest
go get gorm.io/driver/postgres@latest
go mod tidy
```

- [ ] **Step 2: Verify everything compiles**

Run: `go build ./...`

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add frame, gorm, natspubsub dependencies"
```

---

## Task 15: Dockerfiles + .dockerignore

**Files:**
- Create: `apps/crawler/Dockerfile`
- Create: `apps/scheduler/Dockerfile`
- Create: `apps/api/Dockerfile`
- Create: `.dockerignore`

- [ ] **Step 1: Create crawler Dockerfile**

Create `apps/crawler/Dockerfile`:

```dockerfile
ARG TARGETOS=linux
ARG TARGETARCH=amd64

FROM golang:1.26 AS builder

ARG TARGETOS=linux
ARG TARGETARCH=amd64

WORKDIR /app

ARG REPOSITORY
ARG VERSION=dev
ARG REVISION=none
ARG BUILDTIME

COPY go.mod go.sum ./
RUN go mod download

COPY ./apps/crawler ./apps/crawler
COPY ./pkg ./pkg
COPY ./seeds ./seeds

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
     -ldflags="-s -w \
         -X github.com/pitabwire/frame/version.Repository=${REPOSITORY} \
         -X github.com/pitabwire/frame/version.Version=${VERSION} \
         -X github.com/pitabwire/frame/version.Commit=${REVISION} \
         -X github.com/pitabwire/frame/version.Date=${BUILDTIME}" \
     -o /app/binary ./apps/crawler/cmd/main.go

FROM cgr.dev/chainguard/static:latest
LABEL maintainer="Bwire Peter <bwire517@gmail.com>"

USER 65532:65532

EXPOSE 80

ARG REPOSITORY
ARG VERSION
ARG REVISION
ARG BUILDTIME
LABEL org.opencontainers.image.title="Stawi Jobs Crawler"
LABEL org.opencontainers.image.version=$VERSION
LABEL org.opencontainers.image.revision=$REVISION
LABEL org.opencontainers.image.created=$BUILDTIME
LABEL org.opencontainers.image.source=$REPOSITORY

WORKDIR /
COPY --from=builder /app/binary /crawler
COPY --from=builder /app/apps/crawler/migrations /migrations
COPY --from=builder /app/seeds /seeds

ENTRYPOINT ["/crawler"]
```

- [ ] **Step 2: Create scheduler and api Dockerfiles**

Same pattern as crawler, adjusting the binary name, COPY paths, title label, and entrypoint.

- [ ] **Step 3: Create .dockerignore**

Create `.dockerignore`:

```
.git
.gitignore
.idea
.vscode
bin
vendor
*.exe
*.dll
*.so
*.dylib
*.test
*.out
*.log
.env
.env.local
.env.example
README.md
LICENSE
*.md
docs
```

- [ ] **Step 4: Commit**

```bash
git add apps/crawler/Dockerfile apps/scheduler/Dockerfile apps/api/Dockerfile .dockerignore
git commit -m "feat: add Dockerfiles following service-profile standard"
```

---

## Task 16: Kubernetes Manifests (antinvestor/deployments)

**Files:** All in `/home/j/code/antinvestor/deployments/manifests/namespaces/opportunities/`

This task creates the full namespace structure in the deployments repo, following the trustage pattern.

- [ ] **Step 1: Create namespace.yaml**

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: opportunities
  labels:
    environment: prod
    gateway-enabled: "true"
```

- [ ] **Step 2: Create kustomization.yaml**

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - namespace.yaml
  - common
  - kustomization_provider.yaml
  - crawler
  - scheduler
  - api
```

- [ ] **Step 3: Create kustomization_provider.yaml**

Three FluxCD Kustomization CRDs for crawler, scheduler, and api — each pointing to its subdirectory. Interval: 5m, timeout: 15m, prune: true, sourceRef: GitRepository flux-system.

- [ ] **Step 4: Create common/ directory**

Following the trustage pattern: `kustomization.yaml`, `flux-image-automation.yaml` (ImageRepository for `ghcr.io/antinvestor/opportunities-crawler`, ImagePolicy semver >= v0.1.0, ImageUpdateAutomation), `image_repository_secret.yaml`, `reference-grant.yaml`, `setup_queue.yaml` (NATS account).

- [ ] **Step 5: Create crawler/ directory**

- `kustomization.yaml` listing resources
- `opportunities-crawler.yaml` — HelmRelease using colony v1.10.3 with:
  - Image: `ghcr.io/antinvestor/opportunities-crawler`
  - Replicas: 3 (no autoscaling)
  - Resources: 200m/512Mi CPU/memory
  - DB credentials from Vault
  - NATS credentials mounted
  - Migration enabled
  - OpenTelemetry enabled
  - Environment: WORKER_CONCURRENCY=4, SEEDS_DIR=/seeds, CRAWL_QUEUE_NAME, CRAWL_QUEUE_URI
- `database.yaml` — CNPG Database `opportunities` on hub cluster, blue/green credentials
- `db-credentials.yaml` — ExternalSecret from Vault
- `queue_setup.yaml` — NATS User + two JetStream streams (crawl, dedupe)

- [ ] **Step 6: Create scheduler/ and api/ directories**

Similar to crawler but with different images, replicas (1 each), and environment variables.

- [ ] **Step 7: Add opportunities to parent kustomization**

Add `opportunities` to the namespaces kustomization in the deployments repo.

- [ ] **Step 8: Commit in deployments repo**

```bash
cd /home/j/code/antinvestor/deployments
git add manifests/namespaces/opportunities/
git commit -m "feat: add opportunities namespace with crawler, scheduler, api services"
```

---

## Task 17: Remove Old Code

**Files:**
- Delete: `cmd/` directory
- Delete: `internal/` directory
- Delete: `api/` directory (empty)
- Modify: `Makefile` — update for new structure

- [ ] **Step 1: Update Makefile**

```makefile
SHELL := /bin/bash

APP_DIRS := apps/crawler apps/scheduler apps/api

.PHONY: deps build test run-crawler run-scheduler run-api

deps:
	go mod tidy

build:
	for app in $(APP_DIRS); do \
		go build -o bin/$$(basename $$app) ./$$app/cmd; \
	done

test:
	go test ./...

run-crawler:
	go run ./apps/crawler/cmd

run-scheduler:
	go run ./apps/scheduler/cmd

run-api:
	go run ./apps/api/cmd
```

- [ ] **Step 2: Remove old directories**

```bash
rm -rf cmd/ internal/ api/
```

- [ ] **Step 3: Verify everything still compiles**

Run: `cd /home/j/code/stawi.opportunities && go build ./...`

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "refactor: remove old cmd/internal structure, update Makefile for apps/ layout"
```

---

## Task 18: Build, Deploy, and Monitor

- [ ] **Step 1: Build Docker images locally**

```bash
cd /home/j/code/stawi.opportunities
docker build -f apps/crawler/Dockerfile -t ghcr.io/antinvestor/opportunities-crawler:v0.1.0 .
docker build -f apps/scheduler/Dockerfile -t ghcr.io/antinvestor/opportunities-scheduler:v0.1.0 .
docker build -f apps/api/Dockerfile -t ghcr.io/antinvestor/opportunities-api:v0.1.0 .
```

- [ ] **Step 2: Push images to GHCR**

```bash
docker push ghcr.io/antinvestor/opportunities-crawler:v0.1.0
docker push ghcr.io/antinvestor/opportunities-scheduler:v0.1.0
docker push ghcr.io/antinvestor/opportunities-api:v0.1.0
```

- [ ] **Step 3: Push deployment manifests**

```bash
cd /home/j/code/antinvestor/deployments
git push origin main
```

FluxCD will pick up the changes and deploy.

- [ ] **Step 4: Verify pods are running**

```bash
kubectl get pods -n opportunities
```

Expected: 3 crawler pods, 1 scheduler pod, 1 api pod — all Running.

- [ ] **Step 5: Check logs for crawl activity**

```bash
kubectl logs -n opportunities -l app=opportunities-crawler --tail=100 -f
```

Look for `crawl_batch_complete` log events with `jobs_accepted > 0`.

- [ ] **Step 6: Check job count**

```bash
kubectl exec -n opportunities deploy/opportunities-api -- /api healthz
```

Or query the database directly:

```bash
kubectl exec -n datastore pod/hub-1 -- psql -U opportunities -d stawi_jobs -c "SELECT count(*) FROM job_variants;"
```

- [ ] **Step 7: Monitor and fix issues**

Watch logs for:
- Connector parsing failures — adjust regex selectors
- Quality gate rejections — check which fields are missing
- Rate limiting (429s) — adjust per-domain rate limits
- Connection errors — verify NATS and Postgres connectivity

---

## Execution Notes

- **Tasks 1-8** are foundation — must complete sequentially
- **Tasks 9, 10, 11** (connectors) can run in parallel via subagents
- **Tasks 12-13** (pipeline + service setup) depend on connectors
- **Task 14** (dependencies) should run early, can be merged with Task 1
- **Tasks 15-16** (Docker + k8s) can run in parallel
- **Task 17** (cleanup) runs after everything compiles
- **Task 18** (deploy) runs last
