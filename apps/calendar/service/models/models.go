// Package models holds calendar persistence models (resource booking plane).
package models

import (
	"time"

	"github.com/pitabwire/frame/v2/data"
)

// Resource is anything bookable: person time, room, equipment, property, etc.
type Resource struct {
	data.BaseModel `gorm:"embedded"`

	Type         string `gorm:"type:varchar(80);not null;index" json:"type"`
	SubjectKind  string `gorm:"type:varchar(80);not null;uniqueIndex:cal_res_subject,priority:1" json:"subject_kind"`
	SubjectID    string `gorm:"type:varchar(160);not null;uniqueIndex:cal_res_subject,priority:2" json:"subject_id"`
	DisplayName  string `gorm:"type:text;not null;default:''" json:"display_name"`
	Timezone     string `gorm:"type:varchar(80);not null;default:'UTC'" json:"timezone"`
	Capacity     int    `gorm:"not null;default:1" json:"capacity"`
	Status       string `gorm:"type:varchar(20);not null;default:'active';index" json:"status"`
	MetadataJSON string `gorm:"type:text;not null;default:'{}'" json:"metadata_json"`
}

func (Resource) TableName() string { return "cal_resources" }

const (
	ResourceActive   = "active"
	ResourceDisabled = "disabled"

	SubjectKindProfile  = "profile"
	SubjectKindExternal = "external"
)

// Availability is weekly free windows for a resource.
type Availability struct {
	data.BaseModel `gorm:"embedded"`

	ResourceID     string `gorm:"type:varchar(50);not null;uniqueIndex:cal_avail_res,priority:1" json:"resource_id"`
	Timezone       string `gorm:"type:varchar(80);not null;default:'UTC'" json:"timezone"`
	RulesJSON      string `gorm:"type:text;not null;default:'[]'" json:"rules_json"`
	ExceptionsJSON string `gorm:"type:text;not null;default:'[]'" json:"exceptions_json"`
}

func (Availability) TableName() string { return "cal_availability" }

// WeekRule is one recurring weekly window (local to Timezone).
type WeekRule struct {
	Weekday int    `json:"weekday"`
	Start   string `json:"start"`
	End     string `json:"end"`
}

// ExceptionDay blocks a calendar date (YYYY-MM-DD).
type ExceptionDay struct {
	Date    string `json:"date"`
	Blocked bool   `json:"blocked"`
}

// BusyBlock is a non-bookable interval on a resource.
type BusyBlock struct {
	data.BaseModel `gorm:"embedded"`

	ResourceID string    `gorm:"type:varchar(50);not null;index" json:"resource_id"`
	StartAt    time.Time `gorm:"not null;index" json:"start_at"`
	EndAt      time.Time `gorm:"not null;index" json:"end_at"`
	Source     string    `gorm:"type:varchar(80);not null;default:'manual'" json:"source"`
	Note       string    `gorm:"type:text;not null;default:''" json:"note"`
	// ExternalKey for idempotent import upserts (indexed; uniqueness enforced in repo).
	ExternalKey string `gorm:"type:varchar(200);not null;default:'';index" json:"external_key"`
}

func (BusyBlock) TableName() string { return "cal_busy_blocks" }

// Booking reserves resources over an interval.
type Booking struct {
	data.BaseModel `gorm:"embedded"`

	Status             string     `gorm:"type:varchar(20);not null;default:'confirmed';index" json:"status"`
	StartAt            time.Time  `gorm:"not null;index" json:"start_at"`
	EndAt              time.Time  `gorm:"not null;index" json:"end_at"`
	Source             string     `gorm:"type:varchar(80);not null;default:''" json:"source"`
	SourceRef          string     `gorm:"type:varchar(160);not null;default:''" json:"source_ref"`
	OrganizerProfileID string     `gorm:"type:varchar(80);not null;default:''" json:"organizer_profile_id"`
	Title              string     `gorm:"type:text;not null;default:''" json:"title"`
	Description        string     `gorm:"type:text;not null;default:''" json:"description"`
	Location           string     `gorm:"type:text;not null;default:''" json:"location"`
	IdempotencyKey     string     `gorm:"type:varchar(160);not null;default:'';index" json:"idempotency_key"`
	ICSUID             string     `gorm:"type:varchar(120);not null;default:''" json:"ics_uid"`
	HoldExpiresAt      *time.Time `json:"hold_expires_at,omitempty"`
	MetadataJSON       string     `gorm:"type:text;not null;default:'{}'" json:"metadata_json"`
	CancelReason       string     `gorm:"type:text;not null;default:''" json:"cancel_reason"`
}

func (Booking) TableName() string { return "cal_bookings" }

const (
	BookingHold      = "hold"
	BookingConfirmed = "confirmed"
	BookingCanceled  = "canceled"
)

// BookingLine is one resource (with quantity) on a booking.
type BookingLine struct {
	data.BaseModel `gorm:"embedded"`

	BookingID       string `gorm:"type:varchar(50);not null;index;uniqueIndex:cal_line_booking_res,priority:1" json:"booking_id"`
	ResourceID      string `gorm:"type:varchar(50);not null;index;uniqueIndex:cal_line_booking_res,priority:2" json:"resource_id"`
	Quantity        int    `gorm:"not null;default:1" json:"quantity"`
	ExternalEventID string `gorm:"type:varchar(200);not null;default:''" json:"external_event_id"`
}

func (BookingLine) TableName() string { return "cal_booking_lines" }

// ExternalConnection links a resource to an external calendar for sync.
type ExternalConnection struct {
	data.BaseModel `gorm:"embedded"`

	ResourceID         string     `gorm:"type:varchar(50);not null;index" json:"resource_id"`
	Provider           string     `gorm:"type:varchar(40);not null;index" json:"provider"`
	ExternalCalendarID string     `gorm:"type:varchar(200);not null;default:''" json:"external_calendar_id"`
	CredentialsJSON    string     `gorm:"type:text;not null;default:''" json:"-"`
	ImportBusy         bool       `gorm:"not null;default:true" json:"import_busy"`
	ExportBookings     bool       `gorm:"not null;default:true" json:"export_bookings"`
	Status             string     `gorm:"type:varchar(20);not null;default:'active'" json:"status"`
	SyncToken          string     `gorm:"type:text;not null;default:''" json:"-"`
	LastSyncAt         *time.Time `json:"last_sync_at,omitempty"`
	LastError          string     `gorm:"type:text;not null;default:''" json:"last_error"`
}

func (ExternalConnection) TableName() string { return "cal_external_connections" }

const (
	ConnActive   = "active"
	ConnDisabled = "disabled"
	ConnError    = "error"

	ProviderGoogle    = "google"
	ProviderMicrosoft = "microsoft"
	ProviderCalDAV    = "caldav"
	ProviderLocal     = "local"
)

// SyncOutbox is a durable export intent for external calendars.
type SyncOutbox struct {
	data.BaseModel `gorm:"embedded"`

	ConnectionID string `gorm:"type:varchar(50);not null;index" json:"connection_id"`
	BookingID    string `gorm:"type:varchar(50);not null;index" json:"booking_id"`
	Action       string `gorm:"type:varchar(20);not null" json:"action"` // upsert | delete
	Status       string `gorm:"type:varchar(20);not null;default:'pending';index" json:"status"`
	Attempts     int    `gorm:"not null;default:0" json:"attempts"`
	LastError    string `gorm:"type:text;not null;default:''" json:"last_error"`
}

func (SyncOutbox) TableName() string { return "cal_sync_outbox" }

const (
	OutboxPending = "pending"
	OutboxSent    = "sent"
	OutboxFailed  = "failed"
	ActionUpsert  = "upsert"
	ActionDelete  = "delete"
)

// Slot is a bookable window.
type Slot struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// BusyInterval is API/domain busy range.
type BusyInterval struct {
	ResourceID string
	Start      time.Time
	End        time.Time
	Source     string
	Note       string
}

// Schema for Frame migrate.
func Schema() []any {
	return []any{
		&Resource{},
		&Availability{},
		&BusyBlock{},
		&Booking{},
		&BookingLine{},
		&ExternalConnection{},
		&SyncOutbox{},
	}
}
