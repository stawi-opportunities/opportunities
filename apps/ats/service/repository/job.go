package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/pitabwire/frame/v2/datastore"
	"github.com/pitabwire/frame/v2/datastore/pool"
	"github.com/pitabwire/frame/v2/workerpool"
	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/apps/ats/service/models"
)

type jobRepository struct {
	datastore.BaseRepository[*models.Job]
}

// NewJobRepository constructs a JobRepository on the given pool.
func NewJobRepository(ctx context.Context, dbPool pool.Pool, workMan workerpool.Manager) JobRepository {
	return &jobRepository{
		BaseRepository: datastore.NewBaseRepository[*models.Job](
			ctx, dbPool, workMan, func() *models.Job { return &models.Job{} },
		),
	}
}

func (r *jobRepository) GetInPartition(ctx context.Context, tenantID, partitionID, id string) (*models.Job, error) {
	var j models.Job
	err := r.Pool().DB(ctx, true).
		Where("id = ? AND tenant_id = ? AND partition_id = ?", id, tenantID, partitionID).
		First(&j).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ats: get job: %w", err)
	}
	return &j, nil
}

func (r *jobRepository) ListByPartition(ctx context.Context, tenantID, partitionID, status string, limit int) ([]*models.Job, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := r.Pool().DB(ctx, true).
		Where("tenant_id = ? AND partition_id = ?", tenantID, partitionID).
		Order("created_at DESC").
		Limit(limit)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var out []*models.Job
	if err := q.Find(&out).Error; err != nil {
		return nil, fmt.Errorf("ats: list jobs: %w", err)
	}
	return out, nil
}

func (r *jobRepository) CountByStatus(ctx context.Context, tenantID, partitionID, status string) (int64, error) {
	q := r.Pool().DB(ctx, true).Model(&models.Job{}).
		Where("tenant_id = ? AND partition_id = ?", tenantID, partitionID)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var n int64
	if err := q.Count(&n).Error; err != nil {
		return 0, fmt.Errorf("ats: count jobs: %w", err)
	}
	return n, nil
}
