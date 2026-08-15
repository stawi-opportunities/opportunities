package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/pitabwire/frame/v2/datastore"
	"github.com/pitabwire/frame/v2/datastore/pool"
	"github.com/pitabwire/frame/v2/workerpool"
	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/apps/calendar/service/models"
)

type resourceRepository struct {
	datastore.BaseRepository[*models.Resource]
}

func NewResourceRepository(ctx context.Context, dbPool pool.Pool, workMan workerpool.Manager) ResourceRepository {
	return &resourceRepository{
		BaseRepository: datastore.NewBaseRepository[*models.Resource](
			ctx, dbPool, workMan, func() *models.Resource { return &models.Resource{} },
		),
	}
}

func (r *resourceRepository) GetInPartition(ctx context.Context, tenantID, partitionID, id string) (*models.Resource, error) {
	var m models.Resource
	err := r.Pool().DB(ctx, true).
		Where("id = ? AND tenant_id = ? AND partition_id = ?", id, tenantID, partitionID).
		First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("calendar: get resource: %w", err)
	}
	return &m, nil
}

func (r *resourceRepository) GetBySubject(ctx context.Context, tenantID, partitionID, kind, subjectID string) (*models.Resource, error) {
	var m models.Resource
	err := r.Pool().DB(ctx, true).
		Where("tenant_id = ? AND partition_id = ? AND subject_kind = ? AND subject_id = ?",
			tenantID, partitionID, kind, subjectID).
		First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("calendar: get resource by subject: %w", err)
	}
	return &m, nil
}

func (r *resourceRepository) List(ctx context.Context, tenantID, partitionID, typ, subjectKind, subjectID string, limit int) ([]*models.Resource, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := r.Pool().DB(ctx, true).
		Where("tenant_id = ? AND partition_id = ?", tenantID, partitionID).
		Order("created_at DESC").Limit(limit)
	if typ != "" {
		q = q.Where("type = ?", typ)
	}
	if subjectKind != "" {
		q = q.Where("subject_kind = ?", subjectKind)
	}
	if subjectID != "" {
		q = q.Where("subject_id = ?", subjectID)
	}
	var out []*models.Resource
	if err := q.Find(&out).Error; err != nil {
		return nil, fmt.Errorf("calendar: list resources: %w", err)
	}
	return out, nil
}

type availabilityRepository struct {
	datastore.BaseRepository[*models.Availability]
}

func NewAvailabilityRepository(ctx context.Context, dbPool pool.Pool, workMan workerpool.Manager) AvailabilityRepository {
	return &availabilityRepository{
		BaseRepository: datastore.NewBaseRepository[*models.Availability](
			ctx, dbPool, workMan, func() *models.Availability { return &models.Availability{} },
		),
	}
}

func (r *availabilityRepository) GetByResource(ctx context.Context, tenantID, partitionID, resourceID string) (*models.Availability, error) {
	var a models.Availability
	err := r.Pool().DB(ctx, true).
		Where("tenant_id = ? AND partition_id = ? AND resource_id = ?", tenantID, partitionID, resourceID).
		First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("calendar: get availability: %w", err)
	}
	return &a, nil
}

func (r *availabilityRepository) UpsertForResource(ctx context.Context, a *models.Availability) error {
	existing, err := r.GetByResource(ctx, a.TenantID, a.PartitionID, a.ResourceID)
	if err != nil {
		return err
	}
	if existing == nil {
		return r.Create(ctx, a)
	}
	existing.Timezone = a.Timezone
	existing.RulesJSON = a.RulesJSON
	existing.ExceptionsJSON = a.ExceptionsJSON
	_, err = r.Update(ctx, existing, "timezone", "rules_json", "exceptions_json", "modified_at", "modified_by")
	if err != nil {
		return fmt.Errorf("calendar: update availability: %w", err)
	}
	*a = *existing
	return nil
}
