package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// CandidateSessionRepository wraps GORM operations for CandidateSession.
//
// The repository is intentionally crypto-naive: it stores and returns the
// encrypted byte fields untouched. Encryption is the Store layer's job
// (pkg/authsession.Store). Splitting the concerns keeps this file
// straightforward to audit and lets the Store be tested without a
// database.
type CandidateSessionRepository struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

// NewCandidateSessionRepository creates a new CandidateSessionRepository.
func NewCandidateSessionRepository(db func(ctx context.Context, readOnly bool) *gorm.DB) *CandidateSessionRepository {
	return &CandidateSessionRepository{db: db}
}

// Upsert replaces any live row for (candidate_id, source_type) with the
// given session. The partial unique index allows exactly one live row,
// so we soft-delete the prior one in the same transaction before
// inserting. Re-capturing on every cookie change keeps the table small
// (one live row per candidate × source) and gives us a clean audit
// trail in the deleted_at column.
func (r *CandidateSessionRepository) Upsert(ctx context.Context, s *domain.CandidateSession) error {
	return r.db(ctx, false).Transaction(func(tx *gorm.DB) error {
		if err := tx.
			Model(&domain.CandidateSession{}).
			Where("candidate_id = ? AND source_type = ? AND deleted_at IS NULL AND revoked_at IS NULL",
				s.CandidateID, s.SourceType).
			Update("deleted_at", time.Now().UTC()).Error; err != nil {
			return err
		}
		return tx.Create(s).Error
	})
}

// GetActive returns the live row for (candidate_id, source_type) or
// (nil, nil) if none exists. "Live" = not soft-deleted, not revoked.
// Expiry is the caller's concern (authsession.IsRowExpired).
func (r *CandidateSessionRepository) GetActive(ctx context.Context, candidateID string, sourceType domain.SourceType) (*domain.CandidateSession, error) {
	var s domain.CandidateSession
	err := r.db(ctx, true).
		Where("candidate_id = ? AND source_type = ? AND revoked_at IS NULL",
			candidateID, sourceType).
		First(&s).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

// ListForCandidate returns all live rows for the candidate, ordered by
// source_type for a stable UI render. Powers the "Connected Accounts"
// page.
func (r *CandidateSessionRepository) ListForCandidate(ctx context.Context, candidateID string) ([]*domain.CandidateSession, error) {
	var out []*domain.CandidateSession
	err := r.db(ctx, true).
		Where("candidate_id = ? AND revoked_at IS NULL", candidateID).
		Order("source_type ASC").
		Find(&out).Error
	return out, err
}

// MarkUsed stamps last_used_at to "now". Best-effort: a failure here
// must not block the replay path, so the Store logs and proceeds when
// this errors.
func (r *CandidateSessionRepository) MarkUsed(ctx context.Context, id string) error {
	return r.db(ctx, false).
		Model(&domain.CandidateSession{}).
		Where("id = ?", id).
		Update("last_used_at", time.Now().UTC()).Error
}

// Revoke stamps revoked_at on the live row for (candidate_id, source_type).
// Re-capture from the extension will create a fresh row; the index allows
// it because the revoked row is excluded.
func (r *CandidateSessionRepository) Revoke(ctx context.Context, candidateID string, sourceType domain.SourceType) error {
	return r.db(ctx, false).
		Model(&domain.CandidateSession{}).
		Where("candidate_id = ? AND source_type = ? AND revoked_at IS NULL AND deleted_at IS NULL",
			candidateID, sourceType).
		Update("revoked_at", time.Now().UTC()).Error
}

// RevokeAll stamps revoked_at on every live row for the candidate. Used
// by the "Disconnect extension" flow — one click drops every captured
// session.
func (r *CandidateSessionRepository) RevokeAll(ctx context.Context, candidateID string) error {
	return r.db(ctx, false).
		Model(&domain.CandidateSession{}).
		Where("candidate_id = ? AND revoked_at IS NULL AND deleted_at IS NULL", candidateID).
		Update("revoked_at", time.Now().UTC()).Error
}
