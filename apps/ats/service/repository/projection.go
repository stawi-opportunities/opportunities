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

type jobProjectionRepository struct {
	datastore.BaseRepository[*models.JobProjection]
}

// NewJobProjectionRepository constructs a JobProjectionRepository.
func NewJobProjectionRepository(ctx context.Context, dbPool pool.Pool, workMan workerpool.Manager) JobProjectionRepository {
	return &jobProjectionRepository{
		BaseRepository: datastore.NewBaseRepository[*models.JobProjection](
			ctx, dbPool, workMan, func() *models.JobProjection { return &models.JobProjection{} },
		),
	}
}

func (r *jobProjectionRepository) UpsertPublished(ctx context.Context, p *models.JobProjection) error {
	if p == nil {
		return fmt.Errorf("ats: nil projection")
	}
	existing, err := r.GetByJob(ctx, p.TenantID, p.PartitionID, p.JobID)
	if err != nil {
		return err
	}
	if existing == nil {
		return r.Create(ctx, p)
	}
	existing.OpportunityID = p.OpportunityID
	existing.Title = p.Title
	existing.Description = p.Description
	existing.Location = p.Location
	existing.Status = "published"
	existing.PublishedAt = p.PublishedAt
	_, err = r.Update(ctx, existing,
		"opportunity_id", "title", "description", "location", "status", "published_at",
		"modified_at", "modified_by")
	if err != nil {
		return fmt.Errorf("ats: update projection: %w", err)
	}
	*p = *existing
	return nil
}

func (r *jobProjectionRepository) MarkUnpublished(ctx context.Context, tenantID, partitionID, jobID string) error {
	existing, err := r.GetByJob(ctx, tenantID, partitionID, jobID)
	if err != nil {
		return err
	}
	if existing == nil {
		return nil
	}
	existing.Status = "unpublished"
	_, err = r.Update(ctx, existing, "status", "modified_at", "modified_by")
	if err != nil {
		return fmt.Errorf("ats: unpublish projection: %w", err)
	}
	return nil
}

func (r *jobProjectionRepository) GetByJob(ctx context.Context, tenantID, partitionID, jobID string) (*models.JobProjection, error) {
	var p models.JobProjection
	err := r.Pool().DB(ctx, true).
		Where("tenant_id = ? AND partition_id = ? AND job_id = ?", tenantID, partitionID, jobID).
		First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ats: get projection: %w", err)
	}
	return &p, nil
}

type idempotencyRepository struct {
	datastore.BaseRepository[*models.IdempotencyRecord]
}

// NewIdempotencyRepository constructs an IdempotencyRepository.
func NewIdempotencyRepository(ctx context.Context, dbPool pool.Pool, workMan workerpool.Manager) IdempotencyRepository {
	return &idempotencyRepository{
		BaseRepository: datastore.NewBaseRepository[*models.IdempotencyRecord](
			ctx, dbPool, workMan, func() *models.IdempotencyRecord { return &models.IdempotencyRecord{} },
		),
	}
}

func (r *idempotencyRepository) Get(ctx context.Context, tenantID, key, route string) (*models.IdempotencyRecord, error) {
	var rec models.IdempotencyRecord
	err := r.Pool().DB(ctx, true).
		Where("tenant_id = ? AND key = ? AND route = ?", tenantID, key, route).
		First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ats: get idempotency: %w", err)
	}
	return &rec, nil
}

func (r *idempotencyRepository) Save(ctx context.Context, rec *models.IdempotencyRecord) error {
	if rec == nil {
		return fmt.Errorf("ats: nil idempotency record")
	}
	existing, err := r.Get(ctx, rec.TenantID, rec.Key, rec.Route)
	if err != nil {
		return err
	}
	if existing != nil {
		existing.Response = rec.Response
		existing.StatusCode = rec.StatusCode
		_, err = r.Update(ctx, existing, "response", "status_code", "modified_at", "modified_by")
		return err
	}
	return r.Create(ctx, rec)
}
