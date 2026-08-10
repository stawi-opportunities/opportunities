package repository

import (
	"context"
	"time"

	"github.com/pitabwire/frame/v2/datastore"

	"github.com/stawi-opportunities/opportunities/apps/ats/service/models"
)

// JobRepository manages employer jobs.
type JobRepository interface {
	datastore.BaseRepository[*models.Job]
	ListByPartition(ctx context.Context, tenantID, partitionID, status string, limit int) ([]*models.Job, error)
	GetInPartition(ctx context.Context, tenantID, partitionID, id string) (*models.Job, error)
	CountByStatus(ctx context.Context, tenantID, partitionID, status string) (int64, error)
}

// ApplicationRepository manages pipeline applications.
type ApplicationRepository interface {
	datastore.BaseRepository[*models.Application]
	GetInPartition(ctx context.Context, tenantID, partitionID, id string) (*models.Application, error)
	GetActive(ctx context.Context, tenantID, partitionID, jobID, profileID string) (*models.Application, error)
	ListByJob(ctx context.Context, tenantID, partitionID, jobID, stage string, limit int) ([]*models.Application, error)
	ListByProfile(ctx context.Context, tenantID, partitionID, profileID string) ([]*models.Application, error)
	CountByStatus(ctx context.Context, tenantID, partitionID, status string) (int64, error)
}

// StageEventRepository appends stage history.
type StageEventRepository interface {
	datastore.BaseRepository[*models.StageEvent]
}

// AvailabilityRepository manages interviewer windows.
type AvailabilityRepository interface {
	datastore.BaseRepository[*models.Availability]
	GetByProfile(ctx context.Context, tenantID, partitionID, profileID string) (*models.Availability, error)
	UpsertForProfile(ctx context.Context, a *models.Availability) error
}

// InterviewRepository manages interviews.
type InterviewRepository interface {
	datastore.BaseRepository[*models.Interview]
	GetInPartition(ctx context.Context, tenantID, partitionID, id string) (*models.Interview, error)
	ListByApplication(ctx context.Context, tenantID, partitionID, applicationID string) ([]*models.Interview, error)
	ListUpcoming(ctx context.Context, tenantID, partitionID string, from, to time.Time, limit int) ([]*models.Interview, error)
	ListScheduledBusy(ctx context.Context, tenantID, partitionID string, from, to time.Time) ([]*models.Interview, error)
	CountInRange(ctx context.Context, tenantID, partitionID string, from, to time.Time) (int64, error)
}

// HireOutcomeRepository stores results-billing rows.
type HireOutcomeRepository interface {
	datastore.BaseRepository[*models.HireOutcome]
	GetByApplication(ctx context.Context, applicationID string) (*models.HireOutcome, error)
}

// OutboxRepository stores notification intents.
type OutboxRepository interface {
	datastore.BaseRepository[*models.OutboxMessage]
}

// AiRunRepository stores AI audit rows.
type AiRunRepository interface {
	datastore.BaseRepository[*models.AiRun]
}
