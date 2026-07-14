package jobqueue

import (
	"encoding/json"
	"time"
)

// QueueRecord is mutable leased work. It deliberately remains an ordinary
// PostgreSQL table because workers update claims, attempts, and terminal state.
type QueueRecord struct {
	ID             string          `gorm:"primaryKey;type:varchar(20)"`
	VariantID      string          `gorm:"type:varchar(20);not null;uniqueIndex"`
	SourceID       string          `gorm:"type:varchar(20);not null;index"`
	CrawlRunID     *string         `gorm:"type:varchar(20)"`
	CrawlJobID     *string         `gorm:"type:varchar(20)"`
	IdempotencyKey string          `gorm:"type:text;not null;uniqueIndex"`
	Payload        json.RawMessage `gorm:"type:jsonb;not null"`
	Status         string          `gorm:"type:varchar(16);not null;default:pending;index;check:job_ingest_queue_status_valid,status IN ('pending','processing','retry','processed','rejected','dead')"`
	Attempt        int             `gorm:"not null;default:0"`
	AvailableAt    time.Time       `gorm:"not null;default:now();index"`
	ClaimedAt      *time.Time
	LeaseExpiresAt *time.Time `gorm:"index"`
	ClaimedBy      *string
	LastError      *string
	CreatedAt      time.Time  `gorm:"not null;default:now();index"`
	UpdatedAt      time.Time  `gorm:"not null;default:now()"`
	ProcessedAt    *time.Time `gorm:"index"`
}

func (QueueRecord) TableName() string { return "job_ingest_queue" }

type OpportunityRecord struct {
	CanonicalID    string     `gorm:"primaryKey;type:varchar(20)"`
	Slug           string     `gorm:"type:text;not null;uniqueIndex"`
	Kind           string     `gorm:"type:text;not null;index"`
	SourceID       *string    `gorm:"type:varchar(20);index"`
	Title          string     `gorm:"type:text;not null"`
	Description    *string    `gorm:"type:text"`
	// HowToApply holds paywalled application instructions (Markdown).
	// Omitted from the public jobs API; served only to subscribed members.
	HowToApply     *string    `gorm:"type:text"`
	IssuingEntity  *string    `gorm:"type:text"`
	Country        *string    `gorm:"type:text;index"`
	Region         *string    `gorm:"type:text"`
	City           *string    `gorm:"type:text"`
	Remote         bool       `gorm:"not null;default:false"`
	ApplyURL       string     `gorm:"type:text;not null;check:opportunities_apply_url_required,btrim(apply_url) <> ''"`
	PostedAt       *time.Time `gorm:"index"`
	Deadline       *time.Time `gorm:"index"`
	Currency       *string    `gorm:"type:text"`
	AmountMin      *float64
	AmountMax      *float64
	EmploymentType *string         `gorm:"type:text"`
	Seniority      *string         `gorm:"type:text"`
	GeoScope       *string         `gorm:"type:text"`
	Status         string          `gorm:"type:text;not null;default:active;index"`
	FirstSeenAt    time.Time       `gorm:"not null;default:now()"`
	LastSeenAt     time.Time       `gorm:"not null;default:now();index"`
	Attributes     json.RawMessage `gorm:"type:jsonb;not null;default:'{}'"`
	QualityScore   *float64
	Hidden         bool                      `gorm:"not null;default:false;index"`
	HiddenReason   *string                   `gorm:"type:text"`
	CreatedAt      time.Time                 `gorm:"not null;default:now()"`
	UpdatedAt      time.Time                 `gorm:"not null;default:now()"`
	Sources        []OpportunitySourceRecord `gorm:"foreignKey:CanonicalID;references:CanonicalID;constraint:OnDelete:CASCADE" json:"-"`
}

func (OpportunityRecord) TableName() string { return "opportunities" }

type OpportunityIdentityRecord struct {
	HardKey     string    `gorm:"primaryKey;type:text"`
	CanonicalID string    `gorm:"type:varchar(20);not null;uniqueIndex"`
	CreatedAt   time.Time `gorm:"not null;default:now()"`
}

func (OpportunityIdentityRecord) TableName() string { return "opportunity_identities" }

type OpportunitySourceRecord struct {
	CanonicalID    string    `gorm:"primaryKey;type:varchar(20);not null;index"`
	SourceID       string    `gorm:"primaryKey;type:varchar(20);not null;index"`
	ExternalID     string    `gorm:"primaryKey;type:text;not null;default:''"`
	ApplyURL       string    `gorm:"type:text;not null;check:opportunity_sources_apply_url_required,btrim(apply_url) <> ''"`
	ContentHash    *string   `gorm:"type:varchar(64)"`
	FirstSeenAt    time.Time `gorm:"not null;default:now()"`
	LastSeenAt     time.Time `gorm:"not null;default:now();index"`
	LastCrawlRunID *string   `gorm:"type:varchar(20)"`
	InactiveAfter  *time.Time
	Active         bool `gorm:"not null;default:true;index"`
}

func (OpportunitySourceRecord) TableName() string { return "opportunity_sources" }

// IngestEventRecord is converted to an append-only TimescaleDB hypertable by
// the SQL capability migration after GORM creates its ordinary table shape.
type IngestEventRecord struct {
	EventID    string          `gorm:"primaryKey;type:varchar(20)"`
	OccurredAt time.Time       `gorm:"primaryKey;not null;default:now()"`
	IngestID   string          `gorm:"type:varchar(20);not null;index"`
	VariantID  string          `gorm:"type:varchar(20);not null"`
	SourceID   string          `gorm:"type:varchar(20);not null;index"`
	EventType  string          `gorm:"type:varchar(32);not null;index"`
	Attempt    int             `gorm:"not null;default:0"`
	Details    json.RawMessage `gorm:"type:jsonb;not null;default:'{}'"`
}

func (IngestEventRecord) TableName() string { return "job_ingest_events" }
