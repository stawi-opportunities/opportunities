package repository

import (
	"context"

	"gorm.io/gorm"

	"stawi.jobs/pkg/domain"
)

// RejectedJobRepository wraps GORM operations for the RejectedJob entity.
type RejectedJobRepository struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

// NewRejectedJobRepository creates a new RejectedJobRepository.
func NewRejectedJobRepository(db func(ctx context.Context, readOnly bool) *gorm.DB) *RejectedJobRepository {
	return &RejectedJobRepository{db: db}
}

// Create inserts a new rejected job record.
func (r *RejectedJobRepository) Create(ctx context.Context, rj *domain.RejectedJob) error {
	return r.db(ctx, false).Create(rj).Error
}

// CountBySourceType returns rejection counts grouped by source type.
func (r *RejectedJobRepository) CountBySourceType(ctx context.Context) (map[domain.SourceType]int64, error) {
	type row struct {
		SourceType domain.SourceType
		Count      int64
	}

	// Join rejected_jobs with sources to get the source type.
	var rows []row
	err := r.db(ctx, true).
		Table("rejected_jobs rj").
		Select("s.type AS source_type, COUNT(*) AS count").
		Joins("JOIN sources s ON s.id = rj.source_id").
		Group("s.type").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	result := make(map[domain.SourceType]int64, len(rows))
	for _, row := range rows {
		result[row.SourceType] = row.Count
	}
	return result, nil
}
