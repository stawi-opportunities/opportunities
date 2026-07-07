package applications

import (
	"encoding/json"
	"time"
)

type ApplicationRecord struct {
	ApplicationID string          `gorm:"primaryKey;type:text"`
	CandidateID   string          `gorm:"type:text;not null;uniqueIndex:applications_candidate_opportunity,priority:1;index:applications_candidate_status_idx,priority:1"`
	OpportunityID string          `gorm:"type:text;not null;uniqueIndex:applications_candidate_opportunity,priority:2"`
	MatchID       string          `gorm:"type:text;not null"`
	Status        string          `gorm:"type:text;not null;index:applications_candidate_status_idx,priority:2"`
	CurrentStage  *string         `gorm:"type:text"`
	Metadata      json.RawMessage `gorm:"type:jsonb;not null;default:'{}'"`
	SubmittedAt   *time.Time
	LastEventID   *string   `gorm:"type:text"`
	CreatedAt     time.Time `gorm:"not null;default:now();index:applications_candidate_status_idx,priority:3,sort:desc"`
	UpdatedAt     time.Time `gorm:"not null;default:now()"`
}

func (ApplicationRecord) TableName() string { return "applications" }

type NoteRecord struct {
	NoteID        string     `gorm:"primaryKey;type:text"`
	ApplicationID string     `gorm:"type:text;not null;index:application_notes_app_idx,priority:1,where:deleted_at IS NULL"`
	Body          string     `gorm:"type:text;not null"`
	DeletedAt     *time.Time `gorm:"index:application_notes_app_idx,where:deleted_at IS NULL"`
	CreatedAt     time.Time  `gorm:"not null;default:now();index:application_notes_app_idx,priority:2,sort:desc,where:deleted_at IS NULL"`
	UpdatedAt     time.Time  `gorm:"not null;default:now()"`
}

func (NoteRecord) TableName() string { return "application_notes" }

type AttachmentRecord struct {
	AttachmentID  string     `gorm:"primaryKey;type:text"`
	ApplicationID string     `gorm:"type:text;not null;index:application_attachments_app_idx,priority:1,where:deleted_at IS NULL"`
	R2Key         string     `gorm:"type:text;not null"`
	ContentType   string     `gorm:"type:text;not null"`
	Bytes         int64      `gorm:"not null"`
	Filename      *string    `gorm:"type:text"`
	DeletedAt     *time.Time `gorm:"index:application_attachments_app_idx,where:deleted_at IS NULL"`
	CreatedAt     time.Time  `gorm:"not null;default:now();index:application_attachments_app_idx,priority:2,sort:desc,where:deleted_at IS NULL"`
}

func (AttachmentRecord) TableName() string { return "application_attachments" }

type ReminderRecord struct {
	ReminderID    string    `gorm:"primaryKey;type:text"`
	ApplicationID string    `gorm:"type:text;not null;index:application_reminders_app_idx,priority:1,where:deleted_at IS NULL"`
	DueAt         time.Time `gorm:"not null;index:application_reminders_due_idx,where:status = 'pending' AND deleted_at IS NULL;index:application_reminders_app_idx,priority:2,where:deleted_at IS NULL"`
	Status        string    `gorm:"type:text;not null"`
	Note          *string   `gorm:"type:text"`
	CompletedAt   *time.Time
	DeletedAt     *time.Time
	CreatedAt     time.Time `gorm:"not null;default:now()"`
	UpdatedAt     time.Time `gorm:"not null;default:now()"`
}

func (ReminderRecord) TableName() string { return "application_reminders" }

type IdempotencyRecord struct {
	CandidateID     string          `gorm:"primaryKey;type:text"`
	Key             string          `gorm:"primaryKey;type:text"`
	RouteGroup      string          `gorm:"primaryKey;type:text"`
	StatusCode      int             `gorm:"not null"`
	ResponseBody    json.RawMessage `gorm:"type:jsonb;not null"`
	ResponseHeaders json.RawMessage `gorm:"type:jsonb;not null;default:'{}'"`
	CreatedAt       time.Time       `gorm:"not null;default:now()"`
	ExpiresAt       time.Time       `gorm:"not null;index"`
}

func (IdempotencyRecord) TableName() string { return "idempotency_keys" }

type EventRecord struct {
	EventID       string          `gorm:"primaryKey;type:text"`
	OccurredAt    time.Time       `gorm:"primaryKey;not null;default:now();index:application_events_app_time_idx,priority:2,sort:desc;index:application_events_candidate_time_idx,priority:2,sort:desc"`
	ApplicationID string          `gorm:"type:text;not null;index:application_events_app_time_idx,priority:1"`
	CandidateID   string          `gorm:"type:text;not null;index:application_events_candidate_time_idx,priority:1"`
	Kind          string          `gorm:"type:text;not null"`
	FromStatus    *string         `gorm:"type:text"`
	ToStatus      *string         `gorm:"type:text"`
	Actor         string          `gorm:"type:text;not null"`
	Data          json.RawMessage `gorm:"type:jsonb;not null;default:'{}'"`
}

func (EventRecord) TableName() string { return "application_events" }

func Schema() []any {
	return []any{
		&ApplicationRecord{},
		&NoteRecord{},
		&AttachmentRecord{},
		&ReminderRecord{},
		&IdempotencyRecord{},
		&EventRecord{},
	}
}
