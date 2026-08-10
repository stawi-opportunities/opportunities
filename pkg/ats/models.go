// Package ats is the employer ATS domain: jobs, pipeline applications,
// interviews, and availability. People are profile_id (platform profile);
// tenancy is Frame data.BaseModel tenant_id + partition_id.
package ats

import (
	"time"

	"github.com/pitabwire/frame/v2/data"
)

// Job is an employer requisition. Source of truth for hiring content;
// optional publish projects to the public opportunities board.
type Job struct {
	data.BaseModel `gorm:"embedded"`

	Title         string     `gorm:"type:text;not null"`
	Description   string     `gorm:"type:text;not null;default:''"`
	Location      string     `gorm:"type:text;not null;default:''"`
	Status        string     `gorm:"type:varchar(20);not null;default:'draft';index"`
	Visibility    string     `gorm:"type:varchar(20);not null;default:'private'"`
	OpportunityID string     `gorm:"type:varchar(50);not null;default:''"`
	PublishedAt   *time.Time `gorm:""`
	StageTemplate string     `gorm:"type:text;not null;default:''"` // JSON array of stage keys; empty → DefaultStages()
	MetadataJSON  string     `gorm:"type:text;not null;default:'{}'"`
}

func (Job) TableName() string { return "ats_jobs" }

const (
	JobStatusDraft  = "draft"
	JobStatusOpen   = "open"
	JobStatusClosed = "closed"

	VisibilityPrivate   = "private"
	VisibilityPublished = "published"
)

// Application is a person (profile) in a job pipeline. Not seeker-side pkg/applications.
type Application struct {
	data.BaseModel `gorm:"embedded"`

	JobID       string  `gorm:"type:varchar(50);not null;uniqueIndex:ats_app_job_profile_active,priority:1"`
	ProfileID   string  `gorm:"type:varchar(50);not null;uniqueIndex:ats_app_job_profile_active,priority:2;index"`
	CandidateID string  `gorm:"type:varchar(50);not null;default:''"` // optional matching candidate_profiles.id
	Stage       string  `gorm:"type:varchar(40);not null;index"`
	Source      string  `gorm:"type:varchar(40);not null;default:'manual'"`
	SourceRef   string  `gorm:"type:text;not null;default:''"`
	Status      string  `gorm:"type:varchar(20);not null;default:'active';uniqueIndex:ats_app_job_profile_active,priority:3"`
	Summary     string  `gorm:"type:text;not null;default:''"`
	Score       float32 `gorm:"type:real;not null;default:0"`
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

// StageEvent is an append-only stage transition.
type StageEvent struct {
	data.BaseModel `gorm:"embedded"`

	ApplicationID  string `gorm:"type:varchar(50);not null;index"`
	FromStage      string `gorm:"type:varchar(40);not null"`
	ToStage        string `gorm:"type:varchar(40);not null"`
	ActorProfileID string `gorm:"type:varchar(50);not null"`
	Note           string `gorm:"type:text;not null;default:''"`
}

func (StageEvent) TableName() string { return "ats_stage_events" }

// Availability is weekly interviewer free windows for a profile in a partition.
type Availability struct {
	data.BaseModel `gorm:"embedded"`

	ProfileID      string `gorm:"type:varchar(50);not null;uniqueIndex:ats_avail_profile,priority:1"`
	Timezone       string `gorm:"type:varchar(80);not null;default:'UTC'"`
	RulesJSON      string `gorm:"type:text;not null;default:'[]'"` // []WeekRule
	ExceptionsJSON string `gorm:"type:text;not null;default:'[]'"` // []ExceptionDay
}

func (Availability) TableName() string { return "ats_availability" }

// WeekRule is one recurring weekly window (local to Timezone).
type WeekRule struct {
	// Weekday: 0=Sunday … 6=Saturday (time.Weekday).
	Weekday int    `json:"weekday"`
	Start   string `json:"start"` // "09:00"
	End     string `json:"end"`   // "17:00"
}

// ExceptionDay blocks or opens a calendar date (YYYY-MM-DD) in the profile timezone.
type ExceptionDay struct {
	Date    string `json:"date"`
	Blocked bool   `json:"blocked"`
}

// Interview is scheduled against an application.
type Interview struct {
	data.BaseModel `gorm:"embedded"`

	ApplicationID string     `gorm:"type:varchar(50);not null;index"`
	Type          string     `gorm:"type:varchar(40);not null;default:'general'"`
	DurationMin   int        `gorm:"not null;default:30"`
	PanelJSON     string     `gorm:"type:text;not null;default:'[]'"` // []profile_id
	Status        string     `gorm:"type:varchar(20);not null;default:'proposed';index"`
	SlotStart     *time.Time `gorm:""`
	SlotEnd       *time.Time `gorm:""`
	Location      string     `gorm:"type:text;not null;default:''"`
	VideoURL      string     `gorm:"type:text;not null;default:''"`
	ICSUID        string     `gorm:"type:varchar(80);not null;default:''"`
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

	Kind           string `gorm:"type:varchar(40);not null"`
	PayloadJSON    string `gorm:"type:text;not null"`
	Status         string `gorm:"type:varchar(20);not null;default:'pending';index"`
	IdempotencyKey string `gorm:"type:varchar(120);not null;uniqueIndex"`
	Attempts       int    `gorm:"not null;default:0"`
}

func (OutboxMessage) TableName() string { return "ats_outbox" }

// HireOutcome records results-billing emission (idempotent per application).
type HireOutcome struct {
	data.BaseModel `gorm:"embedded"`

	ApplicationID  string `gorm:"type:varchar(50);not null;uniqueIndex"`
	JobID          string `gorm:"type:varchar(50);not null;index"`
	ProfileID      string `gorm:"type:varchar(50);not null"`
	BillingRef     string `gorm:"type:varchar(120);not null;default:''"`
	IdempotencyKey string `gorm:"type:varchar(120);not null;uniqueIndex"`
}

func (HireOutcome) TableName() string { return "ats_hire_outcomes" }

// AiRun audits AI assist calls.
type AiRun struct {
	data.BaseModel `gorm:"embedded"`

	Purpose        string `gorm:"type:varchar(40);not null"`
	ActorProfileID string `gorm:"type:varchar(50);not null"`
	InputHash      string `gorm:"type:varchar(64);not null;default:''"`
	OutputJSON     string `gorm:"type:text;not null;default:''"`
}

func (AiRun) TableName() string { return "ats_ai_runs" }

// Schema returns all ATS models for AutoMigrate.
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
	}
}
