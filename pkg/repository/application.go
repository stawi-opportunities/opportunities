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

// UpdateStatus transitions a row to its terminal state and stamps
// submitted_at when the new status is "submitted" or "skipped".
//
// Used by the autoapply handler to flip a "pending" reservation row
// into its final outcome after the submitter returns.
func (r *ApplicationRepository) UpdateStatus(ctx context.Context, id, status, method, externalRef string) error {
	updates := map[string]interface{}{
		"status": status,
		"method": method,
	}
	if externalRef != "" {
		updates["response_type"] = externalRef
	}
	if status == domain.AppStatusSubmitted || status == domain.AppStatusSkipped {
		updates["submitted_at"] = time.Now().UTC()
	}
	return r.db(ctx, false).
		Model(&domain.CandidateApplication{}).
		Where("id = ?", id).
		Updates(updates).Error
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

// CountTodayForCandidate counts applications attempted since midnight
// UTC today that count against the per-candidate daily limit.
//
// Both submitted and skipped attempts count: a skip still consumed a
// browser run / LLM call / ATS round-trip, so allowing unbounded skips
// would let one candidate hammer crawled URLs without limit. Failed
// attempts (transient infra errors) are excluded so a flapping queue
// can't burn a candidate's quota.
func (r *ApplicationRepository) CountTodayForCandidate(ctx context.Context, candidateID string) (int, error) {
	now := time.Now().UTC()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	var count int64
	err := r.db(ctx, true).
		Model(&domain.CandidateApplication{}).
		Where("candidate_id = ? AND submitted_at >= ? AND status IN ?",
			candidateID, midnight,
			[]string{domain.AppStatusSubmitted, domain.AppStatusSkipped}).
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
