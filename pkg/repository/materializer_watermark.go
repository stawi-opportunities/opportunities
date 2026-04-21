package repository

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MaterializerWatermark holds the most-recent R2 object key the
// materializer has applied to Manticore for a given collection prefix.
// Stored in Postgres for simplicity; a future phase may migrate to KV.
type MaterializerWatermark struct {
	Prefix    string    `gorm:"type:text;primaryKey"          json:"prefix"`
	LastR2Key string    `gorm:"type:text;not null;default:''" json:"last_r2_key"`
	UpdatedAt time.Time `gorm:"not null;default:now()"        json:"updated_at"`
}

// TableName pins to the migration's table name (plural).
func (MaterializerWatermark) TableName() string { return "materializer_watermarks" }

// WatermarkRepository persists materializer progress per collection prefix.
type WatermarkRepository struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

// NewWatermarkRepository constructs a WatermarkRepository.
func NewWatermarkRepository(db func(ctx context.Context, readOnly bool) *gorm.DB) *WatermarkRepository {
	return &WatermarkRepository{db: db}
}

// Get returns the last-applied R2 key for prefix, or "" if no row
// exists yet (first-boot path).
func (r *WatermarkRepository) Get(ctx context.Context, prefix string) (string, error) {
	var w MaterializerWatermark
	err := r.db(ctx, true).Where("prefix = ?", prefix).First(&w).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", nil
		}
		return "", err
	}
	return w.LastR2Key, nil
}

// Set advances the watermark. Upserts on conflict — safe to call
// from one materializer pod at a time (which is the expected v1
// deployment; concurrent advancement requires a different strategy).
func (r *WatermarkRepository) Set(ctx context.Context, prefix, r2Key string) error {
	w := MaterializerWatermark{Prefix: prefix, LastR2Key: r2Key, UpdatedAt: time.Now()}
	return r.db(ctx, false).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "prefix"}},
			DoUpdates: clause.AssignmentColumns([]string{"last_r2_key", "updated_at"}),
		}).
		Create(&w).Error
}
