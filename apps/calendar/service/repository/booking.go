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

	"github.com/stawi-opportunities/opportunities/apps/calendar/service/models"
)

type busyRepository struct {
	datastore.BaseRepository[*models.BusyBlock]
}

func NewBusyRepository(ctx context.Context, dbPool pool.Pool, workMan workerpool.Manager) BusyRepository {
	return &busyRepository{
		BaseRepository: datastore.NewBaseRepository[*models.BusyBlock](
			ctx, dbPool, workMan, func() *models.BusyBlock { return &models.BusyBlock{} },
		),
	}
}

func (r *busyRepository) ListInRange(ctx context.Context, tenantID, partitionID string, resourceIDs []string, from, to time.Time) ([]*models.BusyBlock, error) {
	if len(resourceIDs) == 0 {
		return nil, nil
	}
	var out []*models.BusyBlock
	err := r.Pool().DB(ctx, true).
		Where("tenant_id = ? AND partition_id = ? AND resource_id IN ? AND start_at < ? AND end_at > ?",
			tenantID, partitionID, resourceIDs, to, from).
		Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("calendar: list busy: %w", err)
	}
	return out, nil
}

func (r *busyRepository) UpsertExternal(ctx context.Context, b *models.BusyBlock) error {
	if b.ExternalKey == "" {
		return r.Create(ctx, b)
	}
	var existing models.BusyBlock
	err := r.Pool().DB(ctx, true).
		Where("tenant_id = ? AND partition_id = ? AND external_key = ?", b.TenantID, b.PartitionID, b.ExternalKey).
		First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return r.Create(ctx, b)
	}
	if err != nil {
		return fmt.Errorf("calendar: busy lookup: %w", err)
	}
	existing.StartAt = b.StartAt
	existing.EndAt = b.EndAt
	existing.Source = b.Source
	existing.Note = b.Note
	existing.ResourceID = b.ResourceID
	_, err = r.Update(ctx, &existing, "start_at", "end_at", "source", "note", "resource_id", "modified_at", "modified_by")
	return err
}

func (r *busyRepository) DeleteBySourcePrefix(ctx context.Context, tenantID, partitionID, resourceID, sourcePrefix string) error {
	return r.Pool().DB(ctx, false).
		Where("tenant_id = ? AND partition_id = ? AND resource_id = ? AND source LIKE ?",
			tenantID, partitionID, resourceID, sourcePrefix+"%").
		Delete(&models.BusyBlock{}).Error
}

type bookingRepository struct {
	datastore.BaseRepository[*models.Booking]
}

func NewBookingRepository(ctx context.Context, dbPool pool.Pool, workMan workerpool.Manager) BookingRepository {
	return &bookingRepository{
		BaseRepository: datastore.NewBaseRepository[*models.Booking](
			ctx, dbPool, workMan, func() *models.Booking { return &models.Booking{} },
		),
	}
}

func (r *bookingRepository) GetInPartition(ctx context.Context, tenantID, partitionID, id string) (*models.Booking, error) {
	var b models.Booking
	err := r.Pool().DB(ctx, true).
		Where("id = ? AND tenant_id = ? AND partition_id = ?", id, tenantID, partitionID).
		First(&b).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("calendar: get booking: %w", err)
	}
	return &b, nil
}

func (r *bookingRepository) GetByIdempotency(ctx context.Context, tenantID, partitionID, key string) (*models.Booking, error) {
	if key == "" {
		return nil, nil
	}
	var b models.Booking
	err := r.Pool().DB(ctx, true).
		Where("tenant_id = ? AND partition_id = ? AND idempotency_key = ?", tenantID, partitionID, key).
		First(&b).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("calendar: get by idempotency: %w", err)
	}
	return &b, nil
}

func (r *bookingRepository) ListOverlapping(ctx context.Context, tenantID, partitionID string, resourceIDs []string, from, to time.Time) ([]*models.Booking, error) {
	if len(resourceIDs) == 0 {
		return nil, nil
	}
	// Join lines for resource filter.
	var bookingIDs []string
	err := r.Pool().DB(ctx, true).Model(&models.BookingLine{}).
		Where("resource_id IN ?", resourceIDs).
		Distinct("booking_id").
		Pluck("booking_id", &bookingIDs).Error
	if err != nil {
		return nil, fmt.Errorf("calendar: list booking lines: %w", err)
	}
	if len(bookingIDs) == 0 {
		return nil, nil
	}
	var out []*models.Booking
	err = r.Pool().DB(ctx, true).
		Where("tenant_id = ? AND partition_id = ? AND id IN ? AND status IN ? AND start_at < ? AND end_at > ?",
			tenantID, partitionID, bookingIDs,
			[]string{models.BookingHold, models.BookingConfirmed},
			to, from).
		Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("calendar: list overlapping bookings: %w", err)
	}
	// Drop expired holds.
	now := time.Now().UTC()
	filtered := out[:0]
	for _, b := range out {
		if b.Status == models.BookingHold && b.HoldExpiresAt != nil && b.HoldExpiresAt.Before(now) {
			continue
		}
		filtered = append(filtered, b)
	}
	return filtered, nil
}

type bookingLineRepository struct {
	datastore.BaseRepository[*models.BookingLine]
}

func NewBookingLineRepository(ctx context.Context, dbPool pool.Pool, workMan workerpool.Manager) BookingLineRepository {
	return &bookingLineRepository{
		BaseRepository: datastore.NewBaseRepository[*models.BookingLine](
			ctx, dbPool, workMan, func() *models.BookingLine { return &models.BookingLine{} },
		),
	}
}

func (r *bookingLineRepository) ListByBooking(ctx context.Context, bookingID string) ([]*models.BookingLine, error) {
	var out []*models.BookingLine
	err := r.Pool().DB(ctx, true).Where("booking_id = ?", bookingID).Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("calendar: list lines: %w", err)
	}
	return out, nil
}

func (r *bookingLineRepository) ListByBookings(ctx context.Context, bookingIDs []string) ([]*models.BookingLine, error) {
	if len(bookingIDs) == 0 {
		return nil, nil
	}
	var out []*models.BookingLine
	err := r.Pool().DB(ctx, true).Where("booking_id IN ?", bookingIDs).Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("calendar: list lines multi: %w", err)
	}
	return out, nil
}

type externalConnectionRepository struct {
	datastore.BaseRepository[*models.ExternalConnection]
}

func NewExternalConnectionRepository(ctx context.Context, dbPool pool.Pool, workMan workerpool.Manager) ExternalConnectionRepository {
	return &externalConnectionRepository{
		BaseRepository: datastore.NewBaseRepository[*models.ExternalConnection](
			ctx, dbPool, workMan, func() *models.ExternalConnection { return &models.ExternalConnection{} },
		),
	}
}

func (r *externalConnectionRepository) GetInPartition(ctx context.Context, tenantID, partitionID, id string) (*models.ExternalConnection, error) {
	var c models.ExternalConnection
	err := r.Pool().DB(ctx, true).
		Where("id = ? AND tenant_id = ? AND partition_id = ?", id, tenantID, partitionID).
		First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("calendar: get connection: %w", err)
	}
	return &c, nil
}

func (r *externalConnectionRepository) ListActive(ctx context.Context, tenantID, partitionID string) ([]*models.ExternalConnection, error) {
	var out []*models.ExternalConnection
	err := r.Pool().DB(ctx, true).
		Where("tenant_id = ? AND partition_id = ? AND status = ?", tenantID, partitionID, models.ConnActive).
		Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("calendar: list connections: %w", err)
	}
	return out, nil
}

func (r *externalConnectionRepository) ListByResource(ctx context.Context, tenantID, partitionID, resourceID string) ([]*models.ExternalConnection, error) {
	var out []*models.ExternalConnection
	err := r.Pool().DB(ctx, true).
		Where("tenant_id = ? AND partition_id = ? AND resource_id = ?", tenantID, partitionID, resourceID).
		Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("calendar: list connections by resource: %w", err)
	}
	return out, nil
}

type syncOutboxRepository struct {
	datastore.BaseRepository[*models.SyncOutbox]
}

func NewSyncOutboxRepository(ctx context.Context, dbPool pool.Pool, workMan workerpool.Manager) SyncOutboxRepository {
	return &syncOutboxRepository{
		BaseRepository: datastore.NewBaseRepository[*models.SyncOutbox](
			ctx, dbPool, workMan, func() *models.SyncOutbox { return &models.SyncOutbox{} },
		),
	}
}

func (r *syncOutboxRepository) ListPending(ctx context.Context, limit int) ([]*models.SyncOutbox, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var out []*models.SyncOutbox
	err := r.Pool().DB(ctx, true).
		Where("status = ?", models.OutboxPending).
		Order("created_at ASC").Limit(limit).Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("calendar: list sync outbox: %w", err)
	}
	return out, nil
}

func (r *syncOutboxRepository) MarkSent(ctx context.Context, id string) error {
	return r.Pool().DB(ctx, false).Model(&models.SyncOutbox{}).
		Where("id = ?", id).
		Updates(map[string]any{"status": models.OutboxSent, "attempts": gorm.Expr("attempts + 1")}).Error
}

func (r *syncOutboxRepository) MarkFailed(ctx context.Context, id string, attempts int, errMsg string) error {
	status := models.OutboxPending
	if attempts >= 10 {
		status = models.OutboxFailed
	}
	return r.Pool().DB(ctx, false).Model(&models.SyncOutbox{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status": status, "attempts": attempts, "last_error": errMsg,
		}).Error
}
