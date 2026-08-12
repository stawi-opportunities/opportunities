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

type applicationRepository struct {
	datastore.BaseRepository[*models.Application]
}

// NewApplicationRepository constructs an ApplicationRepository.
func NewApplicationRepository(ctx context.Context, dbPool pool.Pool, workMan workerpool.Manager) ApplicationRepository {
	return &applicationRepository{
		BaseRepository: datastore.NewBaseRepository[*models.Application](
			ctx, dbPool, workMan, func() *models.Application { return &models.Application{} },
		),
	}
}

func (r *applicationRepository) GetInPartition(ctx context.Context, tenantID, partitionID, id string) (*models.Application, error) {
	var a models.Application
	err := r.Pool().DB(ctx, true).
		Where("id = ? AND tenant_id = ? AND partition_id = ?", id, tenantID, partitionID).
		First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ats: get application: %w", err)
	}
	return &a, nil
}

func (r *applicationRepository) GetActive(ctx context.Context, tenantID, partitionID, jobID, profileID string) (*models.Application, error) {
	var a models.Application
	err := r.Pool().DB(ctx, true).
		Where("tenant_id = ? AND partition_id = ? AND job_id = ? AND profile_id = ? AND status = ?",
			tenantID, partitionID, jobID, profileID, models.AppStatusActive).
		First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ats: get active application: %w", err)
	}
	return &a, nil
}

func (r *applicationRepository) ListByJob(ctx context.Context, tenantID, partitionID, jobID, stage string, limit int) ([]*models.Application, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := r.Pool().DB(ctx, true).
		Where("tenant_id = ? AND partition_id = ? AND job_id = ?", tenantID, partitionID, jobID).
		Order("created_at DESC").
		Limit(limit)
	if stage != "" {
		q = q.Where("stage = ?", stage)
	}
	var out []*models.Application
	if err := q.Find(&out).Error; err != nil {
		return nil, fmt.Errorf("ats: list applications: %w", err)
	}
	return out, nil
}

func (r *applicationRepository) ListByProfile(ctx context.Context, tenantID, partitionID, profileID string) ([]*models.Application, error) {
	var out []*models.Application
	err := r.Pool().DB(ctx, true).
		Where("tenant_id = ? AND partition_id = ? AND profile_id = ?", tenantID, partitionID, profileID).
		Order("created_at DESC").
		Limit(100).
		Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("ats: list apps for profile: %w", err)
	}
	return out, nil
}

func (r *applicationRepository) CountByStatus(ctx context.Context, tenantID, partitionID, status string) (int64, error) {
	q := r.Pool().DB(ctx, true).Model(&models.Application{}).
		Where("tenant_id = ? AND partition_id = ?", tenantID, partitionID)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var n int64
	if err := q.Count(&n).Error; err != nil {
		return 0, fmt.Errorf("ats: count applications: %w", err)
	}
	return n, nil
}

type stageEventRepository struct {
	datastore.BaseRepository[*models.StageEvent]
}

// NewStageEventRepository constructs a StageEventRepository.
func NewStageEventRepository(ctx context.Context, dbPool pool.Pool, workMan workerpool.Manager) StageEventRepository {
	return &stageEventRepository{
		BaseRepository: datastore.NewBaseRepository[*models.StageEvent](
			ctx, dbPool, workMan, func() *models.StageEvent { return &models.StageEvent{} },
		),
	}
}
