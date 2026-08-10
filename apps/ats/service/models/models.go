// Package models holds ATS persistence models. All embed Frame data.BaseModel
// (tenant_id, partition_id, audit). People are profile_id only — no CRM copy.
package models

import (
	"time"

	"github.com/pitabwire/frame/v2/data"
)

// Job is an employer requisition (ATS source of truth).
type Job struct {
	data.BaseModel `gorm:"embedded"`

	Title         string     `gorm:"type:text;not null" json:"title"`
	Description   string     `gorm:"type:text;not null;default:''" json:"description"`
	Location      string     `gorm:"type:text;not null;default:''" json:"location"`
	Status        string     `gorm:"type:varchar(20);not null;default:'draft';index" json:"status"`
	Visibility    string     `gorm:"type:varchar(20);not null;default:'private'" json:"visibility"`
	OpportunityID string     `gorm:"type:varchar(50);not null;default:''" json:"opportunity_id"`
	PublishedAt   *time.Time `json:"published_at,omitempty"`
	StageTemplate string     `gorm:"type:text;not null;default:''" json:"stage_template"`
	MetadataJSON  string     `gorm:"type:text;not null;default:'{}'" json:"metadata_json"`
}

func (Job) TableName() string { return "ats_jobs" }

const (
	JobStatusDraft  = "draft"
	JobStatusOpen   = "open"
	JobStatusClosed = "closed"

	VisibilityPrivate   = "private"
	VisibilityPublished = "published"
)

// Application is a profile in a job pipeline (employer-side only).
type Application struct {
	data.BaseModel `gorm:"embedded"`

	JobID       string  `gorm:"type:varchar(50);not null;uniqueIndex:ats_app_job_profile_active,priority:1" json:"job_id"`
	ProfileID   string  `gorm:"type:varchar(50);not null;uniqueIndex:ats_app_job_profile_active,priority:2;index" json:"profile_id"`
	CandidateID string  `gorm:"type:varchar(50);not null;default:''" json:"candidate_id"`
	Stage       string  `gorm:"type:varchar(40);not null;index" json:"stage"`
	Source      string  `gorm:"type:varchar(40);not null;default:'manual'" json:"source"`
	SourceRef   string  `gorm:"type:text;not null;default:''" json:"source_ref"`
	Status      string  `gorm:"type:varchar(20);not null;default:'active';uniqueIndex:ats_app_job_profile_active,priority:3" json:"status"`
	Summary     string  `gorm:"type:text;not null;default:''" json:"summary"`
	Score       float32 `gorm:"type:real;not null;default:0" json:"score"`
}

func (Application) TableName() string { return "ats_applications" }

const (
	AppStatusActive    = "active"
	AppStatusRejected  = "rejected"
	AppStatusWithdrawn = "withdrawn"
	AppStatusHired     = "hired"

	SourceManual     = "manual"
	SourceUpload     = "upload"
	SourceStawiMatch = "stawi_match"
	SourceApplyForm  = "apply_form"
	SourceAgent      = "agent"
)

// StageEvent is append-only stage history.
type StageEvent struct {
	data.BaseModel `gorm:"embedded"`

	ApplicationID  string `gorm:"type:varchar(50);not null;index" json:"application_id"`
	FromStage      string `gorm:"type:varchar(40);not null" json:"from_stage"`
	ToStage        string `gorm:"type:varchar(40);not null" json:"to_stage"`
	ActorProfileID string `gorm:"type:varchar(50);not null" json:"actor_profile_id"`
	Note           string `gorm:"type:text;not null;default:''" json:"note"`
}

func (StageEvent) TableName() string { return "ats_stage_events" }

// Availability is interviewer weekly free windows.
type Availability struct {
	data.BaseModel `gorm:"embedded"`

	ProfileID      string `gorm:"type:varchar(50);not null;uniqueIndex:ats_avail_profile,priority:1" json:"profile_id"`
	Timezone       string `gorm:"type:varchar(80);not null;default:'UTC'" json:"timezone"`
	RulesJSON      string `gorm:"type:text;not null;default:'[]'" json:"rules_json"`
	ExceptionsJSON string `gorm:"type:text;not null;default:'[]'" json:"exceptions_json"`
}

func (Availability) TableName() string { return "ats_availability" }

// WeekRule is one recurring weekly window (local to Timezone).
type WeekRule struct {
	Weekday int    `json:"weekday"` // 0=Sunday … 6=Saturday
	Start   string `json:"start"`   // "09:00"
	End     string `json:"end"`
}

// ExceptionDay blocks a calendar date (YYYY-MM-DD).
type ExceptionDay struct {
	Date    string `json:"date"`
	Blocked bool   `json:"blocked"`
}

// Interview is scheduled against an application.
type Interview struct {
	data.BaseModel `gorm:"embedded"`

	ApplicationID string     `gorm:"type:varchar(50);not null;index" json:"application_id"`
	Type          string     `gorm:"type:varchar(40);not null;default:'general'" json:"type"`
	DurationMin   int        `gorm:"not null;default:30" json:"duration_min"`
	PanelJSON     string     `gorm:"type:text;not null;default:'[]'" json:"panel_json"`
	Status        string     `gorm:"type:varchar(20);not null;default:'proposed';index" json:"status"`
	SlotStart     *time.Time `json:"slot_start,omitempty"`
	SlotEnd       *time.Time `json:"slot_end,omitempty"`
	Location      string     `gorm:"type:text;not null;default:''" json:"location"`
	VideoURL      string     `gorm:"type:text;not null;default:''" json:"video_url"`
	ICSUID        string     `gorm:"type:varchar(80);not null;default:''" json:"ics_uid"`
}

func (Interview) TableName() string { return "ats_interviews" }

const (
	InterviewProposed  = "proposed"
	InterviewScheduled = "scheduled"
	InterviewCompleted = "completed"
	InterviewCanceled  = "canceled"
	InterviewNoShow    = "no_show"
)

// OutboxMessage is a notification intent (email/ICS).
type OutboxMessage struct {
	data.BaseModel `gorm:"embedded"`

	Kind           string `gorm:"type:varchar(40);not null" json:"kind"`
	PayloadJSON    string `gorm:"type:text;not null" json:"payload_json"`
	Status         string `gorm:"type:varchar(20);not null;default:'pending';index" json:"status"`
	IdempotencyKey string `gorm:"type:varchar(120);not null;uniqueIndex" json:"idempotency_key"`
	Attempts       int    `gorm:"not null;default:0" json:"attempts"`
}

func (OutboxMessage) TableName() string { return "ats_outbox" }

// HireOutcome records results-billing emission (idempotent per application).
type HireOutcome struct {
	data.BaseModel `gorm:"embedded"`

	ApplicationID  string `gorm:"type:varchar(50);not null;uniqueIndex" json:"application_id"`
	JobID          string `gorm:"type:varchar(50);not null;index" json:"job_id"`
	ProfileID      string `gorm:"type:varchar(50);not null" json:"profile_id"`
	BillingRef     string `gorm:"type:varchar(120);not null;default:''" json:"billing_ref"`
	IdempotencyKey string `gorm:"type:varchar(120);not null;uniqueIndex" json:"idempotency_key"`
}

func (HireOutcome) TableName() string { return "ats_hire_outcomes" }

// AiRun audits AI assist calls.
type AiRun struct {
	data.BaseModel `gorm:"embedded"`

	Purpose        string `gorm:"type:varchar(40);not null" json:"purpose"`
	ActorProfileID string `gorm:"type:varchar(50);not null" json:"actor_profile_id"`
	InputHash      string `gorm:"type:varchar(64);not null;default:''" json:"input_hash"`
	OutputJSON     string `gorm:"type:text;not null;default:''" json:"output_json"`
}

func (AiRun) TableName() string { return "ats_ai_runs" }

// JobProjection is the published board projection for an ATS job.
type JobProjection struct {
	data.BaseModel `gorm:"embedded"`

	JobID         string     `gorm:"type:varchar(50);not null;uniqueIndex:ats_proj_job,priority:1" json:"job_id"`
	OpportunityID string     `gorm:"type:varchar(80);not null;uniqueIndex" json:"opportunity_id"`
	Title         string     `gorm:"type:text;not null" json:"title"`
	Description   string     `gorm:"type:text;not null;default:''" json:"description"`
	Location      string     `gorm:"type:text;not null;default:''" json:"location"`
	Status        string     `gorm:"type:varchar(20);not null;default:'published';index" json:"status"` // published|unpublished
	PublishedAt   *time.Time `json:"published_at,omitempty"`
}

func (JobProjection) TableName() string { return "ats_job_projections" }

// IdempotencyRecord stores Connect/API idempotency keys.
type IdempotencyRecord struct {
	data.BaseModel `gorm:"embedded"`

	Key        string `gorm:"type:varchar(160);not null;uniqueIndex:ats_idem_key,priority:1" json:"key"`
	Route      string `gorm:"type:varchar(120);not null;uniqueIndex:ats_idem_key,priority:2" json:"route"`
	Response   string `gorm:"type:text;not null;default:''" json:"response"`
	StatusCode int    `gorm:"not null;default:0" json:"status_code"`
}

func (IdempotencyRecord) TableName() string { return "ats_idempotency_keys" }

const (
	OutboxPending                = "pending"
	OutboxSent                   = "sent"
	OutboxFailed                 = "failed"
	OutboxKindInterviewScheduled = "interview.scheduled"
)

// Schema returns all ATS models for Frame Migrate / AutoMigrate.
func Schema() []any {
	return []any{
		&Job{},
		&Application{},
		&StageEvent{},
		&Availability{},
		&Interview{},
		&OutboxMessage{},
		&HireOutcome{},
		&AiRun{},
		&JobProjection{},
		&IdempotencyRecord{},
	}
}
