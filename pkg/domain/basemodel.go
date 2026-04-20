package domain

import (
	"time"

	"github.com/rs/xid"
	"gorm.io/gorm"
)

// BaseModel is the embed used by every persisted domain model.
//
// xid (20-char base32) is the primary key so IDs are generated in the
// application layer — no database coordination, safe across services
// and horizontal replicas, and stays sortable by creation time. We
// intentionally don't inherit Frame's data.BaseModel because this
// project has no tenant/partition concept and those columns would
// just take space.
//
// Foreign-key columns on dependent models are plain strings with no
// REFERENCES constraint — the crawl/dedupe pipeline is eventually
// consistent and we'd rather fix orphans with a janitor than have
// cascading deletes stall an ingest.
type BaseModel struct {
	ID        string         `gorm:"type:varchar(20);primaryKey" json:"id"`
	CreatedAt time.Time      `gorm:"not null;default:now()" json:"created_at"`
	UpdatedAt time.Time      `gorm:"not null;default:now()" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// BeforeCreate assigns an xid if the caller didn't set one. Callers
// can pre-assign when they need the id before the INSERT (e.g. to
// publish an event that references it) — BeforeCreate respects that.
func (m *BaseModel) BeforeCreate(_ *gorm.DB) error {
	if m.ID == "" {
		m.ID = xid.New().String()
	}
	return nil
}

// NewID returns a fresh xid string. Use when constructing a model
// outside GORM (raw SQL inserts, test fixtures, queue payloads).
func NewID() string {
	return xid.New().String()
}
