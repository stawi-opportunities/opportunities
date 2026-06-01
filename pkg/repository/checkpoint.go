// Package repository: CheckpointRepository persists per-source
// connector iterator state so a crawler that crashes mid-iteration
// resumes from the last successful page on the next NATS redelivery.
//
// Schema lives in apps/crawler/migrations/0001/20260528_0050_crawl_checkpoints.sql.
// One row per (source_id, connector_type) — the primary key — holds
// the opaque connector cursor (as JSONB), the page index for operator
// drill-down, the last URL processed, and the last-checkpoint timestamp
// used to detect stale state.
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Checkpoint is the persisted iterator state. Cursor is opaque JSON
// owned by the connector — the repo passes it through as
// json.RawMessage so adding new connectors with their own cursor
// shapes doesn't require schema migrations.
type Checkpoint struct {
	SourceID         string          `gorm:"column:source_id;primaryKey"`
	ConnectorType    string          `gorm:"column:connector_type;primaryKey"`
	Cursor           json.RawMessage `gorm:"column:cursor;type:jsonb"`
	PageIdx          int             `gorm:"column:page_idx"`
	LastURL          string          `gorm:"column:last_url"`
	LastCheckpointAt time.Time       `gorm:"column:last_checkpoint_at"`
}

// TableName binds the struct to the migration-owned crawl_checkpoints
// table so GORM doesn't pluralise it to crawl_checkpointses.
func (Checkpoint) TableName() string { return "crawl_checkpoints" }

// CheckpointRepository persists per-source iterator checkpoints.
//
// Stale checkpoints (>StaleAfter) are returned with isStale=true so
// callers can choose to discard. The default StaleAfter is 6h — long
// enough to ride out a long redelivery window, short enough that a
// listing's page state has likely not shifted underneath us.
type CheckpointRepository struct {
	db         func(ctx context.Context, readOnly bool) *gorm.DB
	StaleAfter time.Duration
}

// NewCheckpointRepository wires the repo with the standard pool
// factory shape every other repository here uses.
func NewCheckpointRepository(db func(ctx context.Context, readOnly bool) *gorm.DB) *CheckpointRepository {
	return &CheckpointRepository{db: db, StaleAfter: 6 * time.Hour}
}

// Get returns the checkpoint for (sourceID, connectorType) or
// (nil, false, nil) when absent. isStale=true means the row exists
// but is older than StaleAfter — caller may discard and start fresh.
func (r *CheckpointRepository) Get(ctx context.Context, sourceID, connectorType string) (*Checkpoint, bool, error) {
	var cp Checkpoint
	err := r.db(ctx, true).
		Where("source_id = ? AND connector_type = ?", sourceID, connectorType).
		First(&cp).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("checkpoint get: %w", err)
	}
	isStale := time.Since(cp.LastCheckpointAt) > r.StaleAfter
	return &cp, isStale, nil
}

// Put upserts the checkpoint. cursor is the connector's serialized
// cursor (its own shape) — passed through as raw JSON.
func (r *CheckpointRepository) Put(ctx context.Context, sourceID, connectorType string, cursor []byte, pageIdx int, lastURL string) error {
	cp := Checkpoint{
		SourceID:         sourceID,
		ConnectorType:    connectorType,
		Cursor:           cursor,
		PageIdx:          pageIdx,
		LastURL:          lastURL,
		LastCheckpointAt: time.Now().UTC(),
	}
	return r.db(ctx, false).Save(&cp).Error
}

// Clear removes the checkpoint — used when a crawl completes
// successfully so the next crawl starts fresh (rather than
// resuming from page N+1 of a now-stale listing).
func (r *CheckpointRepository) Clear(ctx context.Context, sourceID, connectorType string) error {
	return r.db(ctx, false).
		Where("source_id = ? AND connector_type = ?", sourceID, connectorType).
		Delete(&Checkpoint{}).Error
}

// ListAll returns every checkpoint row ordered by source then connector.
// Used by the admin /admin/checkpoints endpoint for operator drill-down.
func (r *CheckpointRepository) ListAll(ctx context.Context) ([]Checkpoint, error) {
	var out []Checkpoint
	err := r.db(ctx, true).
		Order("source_id, connector_type").
		Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("checkpoint list-all: %w", err)
	}
	return out, nil
}

// ListBySource returns every checkpoint row for one source (typically
// one row per connector type the source has crawled, but multi-kind
// sources may produce more).
func (r *CheckpointRepository) ListBySource(ctx context.Context, sourceID string) ([]Checkpoint, error) {
	var out []Checkpoint
	err := r.db(ctx, true).
		Where("source_id = ?", sourceID).
		Order("connector_type").
		Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("checkpoint list-by-source: %w", err)
	}
	return out, nil
}
