package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/pitabwire/frame/v2/datastore"
	"github.com/pitabwire/frame/v2/datastore/pool"
	"github.com/pitabwire/frame/v2/workerpool"
	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/apps/ats/service/models"
)

type availabilityRepository struct {
	datastore.BaseRepository[*models.Availability]
}

// NewAvailabilityRepository constructs an AvailabilityRepository.
func NewAvailabilityRepository(ctx context.Context, dbPool pool.Pool, workMan workerpool.Manager) AvailabilityRepository {
	return &availabilityRepository{
		BaseRepository: datastore.NewBaseRepository[*models.Availability](
			ctx, dbPool, workMan, func() *models.Availability { return &models.Availability{} },
		),
	}
}

func (r *availabilityRepository) GetByProfile(ctx context.Context, tenantID, partitionID, profileID string) (*models.Availability, error) {
	var a models.Availability
	err := r.Pool().DB(ctx, true).
		Where("tenant_id = ? AND partition_id = ? AND profile_id = ?", tenantID, partitionID, profileID).
		First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ats: get availability: %w", err)
	}
	return &a, nil
}

func (r *availabilityRepository) UpsertForProfile(ctx context.Context, a *models.Availability) error {
	existing, err := r.GetByProfile(ctx, a.TenantID, a.PartitionID, a.ProfileID)
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
		return fmt.Errorf("ats: update availability: %w", err)
	}
	*a = *existing
	return nil
}

type interviewRepository struct {
	datastore.BaseRepository[*models.Interview]
}

// NewInterviewRepository constructs an InterviewRepository.
func NewInterviewRepository(ctx context.Context, dbPool pool.Pool, workMan workerpool.Manager) InterviewRepository {
	return &interviewRepository{
		BaseRepository: datastore.NewBaseRepository[*models.Interview](
			ctx, dbPool, workMan, func() *models.Interview { return &models.Interview{} },
		),
	}
}

func (r *interviewRepository) GetInPartition(ctx context.Context, tenantID, partitionID, id string) (*models.Interview, error) {
	var iv models.Interview
	err := r.Pool().DB(ctx, true).
		Where("id = ? AND tenant_id = ? AND partition_id = ?", id, tenantID, partitionID).
		First(&iv).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ats: get interview: %w", err)
	}
	return &iv, nil
}

func (r *interviewRepository) ListByApplication(ctx context.Context, tenantID, partitionID, applicationID string) ([]*models.Interview, error) {
	var out []*models.Interview
	err := r.Pool().DB(ctx, true).
		Where("tenant_id = ? AND partition_id = ? AND application_id = ?", tenantID, partitionID, applicationID).
		Order("created_at DESC").
		Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("ats: list interviews: %w", err)
	}
	return out, nil
}

func (r *interviewRepository) ListUpcoming(ctx context.Context, tenantID, partitionID string, from, to time.Time, limit int) ([]*models.Interview, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var out []*models.Interview
	err := r.Pool().DB(ctx, true).
		Where("tenant_id = ? AND partition_id = ? AND status = ? AND slot_start IS NOT NULL AND slot_start >= ? AND slot_start < ?",
			tenantID, partitionID, models.InterviewScheduled, from, to).
		Order("slot_start ASC").
		Limit(limit).
		Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("ats: list upcoming interviews: %w", err)
	}
	return out, nil
}

func (r *interviewRepository) ListScheduledBusy(ctx context.Context, tenantID, partitionID string, from, to time.Time) ([]*models.Interview, error) {
	var out []*models.Interview
	err := r.Pool().DB(ctx, true).
		Where("tenant_id = ? AND partition_id = ? AND status = ? AND slot_start IS NOT NULL AND slot_end IS NOT NULL AND slot_start < ? AND slot_end > ?",
			tenantID, partitionID, models.InterviewScheduled, to, from).
		Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("ats: list busy: %w", err)
	}
	return out, nil
}

func (r *interviewRepository) CountInRange(ctx context.Context, tenantID, partitionID string, from, to time.Time) (int64, error) {
	var n int64
	err := r.Pool().DB(ctx, true).Model(&models.Interview{}).
		Where("tenant_id = ? AND partition_id = ? AND status = ? AND slot_start >= ? AND slot_start < ?",
			tenantID, partitionID, models.InterviewScheduled, from, to).
		Count(&n).Error
	if err != nil {
		return 0, fmt.Errorf("ats: count interviews: %w", err)
	}
	return n, nil
}

type hireOutcomeRepository struct {
	datastore.BaseRepository[*models.HireOutcome]
}

// NewHireOutcomeRepository constructs a HireOutcomeRepository.
func NewHireOutcomeRepository(ctx context.Context, dbPool pool.Pool, workMan workerpool.Manager) HireOutcomeRepository {
	return &hireOutcomeRepository{
		BaseRepository: datastore.NewBaseRepository[*models.HireOutcome](
			ctx, dbPool, workMan, func() *models.HireOutcome { return &models.HireOutcome{} },
		),
	}
}

func (r *hireOutcomeRepository) GetByApplication(ctx context.Context, applicationID string) (*models.HireOutcome, error) {
	var h models.HireOutcome
	err := r.Pool().DB(ctx, true).Where("application_id = ?", applicationID).First(&h).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ats: get hire outcome: %w", err)
	}
	return &h, nil
}

type outboxRepository struct {
	datastore.BaseRepository[*models.OutboxMessage]
}

// NewOutboxRepository constructs an OutboxRepository.
func NewOutboxRepository(ctx context.Context, dbPool pool.Pool, workMan workerpool.Manager) OutboxRepository {
	return &outboxRepository{
		BaseRepository: datastore.NewBaseRepository[*models.OutboxMessage](
			ctx, dbPool, workMan, func() *models.OutboxMessage { return &models.OutboxMessage{} },
		),
	}
}

func (r *outboxRepository) ListPending(ctx context.Context, limit int) ([]*models.OutboxMessage, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var out []*models.OutboxMessage
	err := r.Pool().DB(ctx, true).
		Where("status = ?", models.OutboxPending).
		Order("created_at ASC").
		Limit(limit).
		Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("ats: list pending outbox: %w", err)
	}
	return out, nil
}

func (r *outboxRepository) MarkSent(ctx context.Context, id string) error {
	res := r.Pool().DB(ctx, false).Model(&models.OutboxMessage{}).
		Where("id = ? AND status = ?", id, models.OutboxPending).
		Updates(map[string]any{
			"status":   models.OutboxSent,
			"attempts": gorm.Expr("attempts + 1"),
		})
	if res.Error != nil {
		return fmt.Errorf("ats: mark outbox sent: %w", res.Error)
	}
	return nil
}

func (r *outboxRepository) MarkFailed(ctx context.Context, id string, attempts int) error {
	status := models.OutboxPending
	if attempts >= 8 {
		status = models.OutboxFailed
	}
	res := r.Pool().DB(ctx, false).Model(&models.OutboxMessage{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":   status,
			"attempts": attempts,
		})
	if res.Error != nil {
		return fmt.Errorf("ats: mark outbox failed: %w", res.Error)
	}
	return nil
}

type aiRunRepository struct {
	datastore.BaseRepository[*models.AiRun]
}

// NewAiRunRepository constructs an AiRunRepository.
func NewAiRunRepository(ctx context.Context, dbPool pool.Pool, workMan workerpool.Manager) AiRunRepository {
	return &aiRunRepository{
		BaseRepository: datastore.NewBaseRepository[*models.AiRun](
			ctx, dbPool, workMan, func() *models.AiRun { return &models.AiRun{} },
		),
	}
}
