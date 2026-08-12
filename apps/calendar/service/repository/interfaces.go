package repository

import (
	"context"
	"time"

	"github.com/pitabwire/frame/v2/datastore"

	"github.com/stawi-opportunities/opportunities/apps/calendar/service/models"
)

type ResourceRepository interface {
	datastore.BaseRepository[*models.Resource]
	GetInPartition(ctx context.Context, tenantID, partitionID, id string) (*models.Resource, error)
	GetBySubject(ctx context.Context, tenantID, partitionID, kind, subjectID string) (*models.Resource, error)
	List(ctx context.Context, tenantID, partitionID, typ, subjectKind, subjectID string, limit int) ([]*models.Resource, error)
}

type AvailabilityRepository interface {
	datastore.BaseRepository[*models.Availability]
	GetByResource(ctx context.Context, tenantID, partitionID, resourceID string) (*models.Availability, error)
	UpsertForResource(ctx context.Context, a *models.Availability) error
}

type BusyRepository interface {
	datastore.BaseRepository[*models.BusyBlock]
	ListInRange(ctx context.Context, tenantID, partitionID string, resourceIDs []string, from, to time.Time) ([]*models.BusyBlock, error)
	UpsertExternal(ctx context.Context, b *models.BusyBlock) error
	DeleteBySourcePrefix(ctx context.Context, tenantID, partitionID, resourceID, sourcePrefix string) error
}

type BookingRepository interface {
	datastore.BaseRepository[*models.Booking]
	GetInPartition(ctx context.Context, tenantID, partitionID, id string) (*models.Booking, error)
	GetByIdempotency(ctx context.Context, tenantID, partitionID, key string) (*models.Booking, error)
	ListOverlapping(ctx context.Context, tenantID, partitionID string, resourceIDs []string, from, to time.Time) ([]*models.Booking, error)
}

type BookingLineRepository interface {
	datastore.BaseRepository[*models.BookingLine]
	ListByBooking(ctx context.Context, bookingID string) ([]*models.BookingLine, error)
	ListByBookings(ctx context.Context, bookingIDs []string) ([]*models.BookingLine, error)
}

type ExternalConnectionRepository interface {
	datastore.BaseRepository[*models.ExternalConnection]
	GetInPartition(ctx context.Context, tenantID, partitionID, id string) (*models.ExternalConnection, error)
	ListActive(ctx context.Context, tenantID, partitionID string) ([]*models.ExternalConnection, error)
	ListByResource(ctx context.Context, tenantID, partitionID, resourceID string) ([]*models.ExternalConnection, error)
}

type SyncOutboxRepository interface {
	datastore.BaseRepository[*models.SyncOutbox]
	ListPending(ctx context.Context, limit int) ([]*models.SyncOutbox, error)
	MarkSent(ctx context.Context, id string) error
	MarkFailed(ctx context.Context, id string, attempts int, errMsg string) error
}
