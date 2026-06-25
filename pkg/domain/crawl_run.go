package domain

import (
	"encoding/json"
	"time"
)

// CrawlRunStatus is the lifecycle of a single full-pass crawl attempt.
type CrawlRunStatus string

const (
	// CrawlRunRunning — the run is actively being driven slice by slice.
	CrawlRunRunning CrawlRunStatus = "running"
	// CrawlRunPaused — progress persisted but no slice in flight (e.g. a
	// continuation deferred by backpressure). The watchdog re-drives it.
	CrawlRunPaused CrawlRunStatus = "paused"
	// CrawlRunCompleted — the iterator reached the end of the board.
	CrawlRunCompleted CrawlRunStatus = "completed"
	// CrawlRunFailed — terminal error or stuck past the attempt ceiling.
	CrawlRunFailed CrawlRunStatus = "failed"
)

// ActiveCrawlRunStatuses are the statuses the single-flight partial unique
// index (idx_crawl_runs_active) covers — at most one of these per source.
var ActiveCrawlRunStatuses = []CrawlRunStatus{CrawlRunRunning, CrawlRunPaused}

// CrawlRun is the durable source of truth for resuming a crawl. A crawl is
// driven in bounded slices (K pages OR T seconds per NATS message); after each
// slice the handler persists the cursor here and self-re-enqueues a
// continuation. On crash/redeploy the lease lapses and the watchdog re-drives
// the run from the last persisted cursor — so a source with millions of jobs
// makes durable progress and never restarts from zero.
//
// It subsumes the old crawl_checkpoints table: cursor is the opaque,
// per-iterator resume state (recipe PageState or a connector's CheckpointState
// cursor), keyed purely by source_id (one active run per source, enforced by
// the partial unique index).
type CrawlRun struct {
	BaseModel

	SourceID string         `gorm:"column:source_id;type:varchar(20);not null;index" json:"source_id"`
	Status   CrawlRunStatus `gorm:"column:status;type:varchar(20);not null;default:'running'" json:"status"`

	// Cursor is opaque JSON owned by the iterator (recipe or connector) — the
	// repo passes it through so new cursor shapes never need a migration.
	Cursor  json.RawMessage `gorm:"column:cursor;type:jsonb" json:"cursor,omitempty"`
	PageIdx int             `gorm:"column:page_idx;not null;default:0" json:"page_idx"`
	LastURL string          `gorm:"column:last_url;type:text" json:"last_url,omitempty"`

	SliceCount   int `gorm:"column:slice_count;not null;default:0" json:"slice_count"`
	JobsFound    int `gorm:"column:jobs_found;not null;default:0" json:"jobs_found"`
	JobsEmitted  int `gorm:"column:jobs_emitted;not null;default:0" json:"jobs_emitted"`
	JobsRejected int `gorm:"column:jobs_rejected;not null;default:0" json:"jobs_rejected"`

	// Owner + LeaseExpiresAt are the single-consumer lease. A slice claims the
	// run (owner = consumer id, lease = now+TTL), renews per page, and releases
	// on yield. A dead owner's lease lapses and the watchdog reclaims.
	Owner          string     `gorm:"column:owner;type:varchar(64)" json:"owner,omitempty"`
	LeaseExpiresAt *time.Time `gorm:"column:lease_expires_at" json:"lease_expires_at,omitempty"`

	Attempt        int       `gorm:"column:attempt;not null;default:0" json:"attempt"`
	ScheduledAt    time.Time `gorm:"column:scheduled_at;not null;default:now()" json:"scheduled_at"`
	StartedAt      time.Time `gorm:"column:started_at;not null;default:now()" json:"started_at"`
	LastProgressAt time.Time `gorm:"column:last_progress_at;not null;default:now()" json:"last_progress_at"`

	CompletedAt  *time.Time `gorm:"column:completed_at" json:"completed_at,omitempty"`
	ErrorCode    string     `gorm:"column:error_code;type:text" json:"error_code,omitempty"`
	ErrorMessage string     `gorm:"column:error_message;type:text" json:"error_message,omitempty"`
}

// TableName binds the model to the migration-owned table.
func (CrawlRun) TableName() string { return "crawl_runs" }
