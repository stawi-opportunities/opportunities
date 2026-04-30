package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// ApplicationRepository wraps GORM operations for CandidateApplication.
type ApplicationRepository struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

// NewApplicationRepository creates a new ApplicationRepository.
func NewApplicationRepository(db func(ctx context.Context, readOnly bool) *gorm.DB) *ApplicationRepository {
	return &ApplicationRepository{db: db}
}

// Create inserts a new application row. The unique partial index on
// (candidate_id, canonical_job_id) WHERE deleted_at IS NULL surfaces
// a duplicate as a constraint violation — callers map that to a skip.
func (r *ApplicationRepository) Create(ctx context.Context, app *domain.CandidateApplication) error {
	return r.db(ctx, false).Create(app).Error
}

// GetByMatchID returns the application for the given match ID, or (nil, nil)
// when no row exists.
func (r *ApplicationRepository) GetByMatchID(ctx context.Context, matchID string) (*domain.CandidateApplication, error) {
	var app domain.CandidateApplication
	err := r.db(ctx, true).
		Where("match_id = ?", matchID).
		First(&app).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &app, nil
}

// ExistsForCandidate reports whether a non-soft-deleted application from
// candidateID on canonicalJobID already exists. Used for fast-path
// idempotency before attempting an insert.
func (r *ApplicationRepository) ExistsForCandidate(ctx context.Context, candidateID, canonicalJobID string) (bool, error) {
	var count int64
	err := r.db(ctx, true).
		Model(&domain.CandidateApplication{}).
		Where("candidate_id = ? AND canonical_job_id = ?", candidateID, canonicalJobID).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// CountTodayForCandidate counts non-failed applications submitted since
// midnight UTC today. Used by the auto-apply daily-limit check.
func (r *ApplicationRepository) CountTodayForCandidate(ctx context.Context, candidateID string) (int, error) {
	now := time.Now().UTC()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	var count int64
	err := r.db(ctx, true).
		Model(&domain.CandidateApplication{}).
		Where("candidate_id = ? AND submitted_at >= ? AND status != ?",
			candidateID, midnight, "failed").
		Count(&count).Error
	return int(count), err
}

// ListForCandidate returns applications for a candidate, most-recent first.
func (r *ApplicationRepository) ListForCandidate(ctx context.Context, candidateID string, limit int) ([]*domain.CandidateApplication, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	var apps []*domain.CandidateApplication
	err := r.db(ctx, true).
		Where("candidate_id = ?", candidateID).
		Order("created_at DESC").
		Limit(limit).
		Find(&apps).Error
	return apps, err
}
