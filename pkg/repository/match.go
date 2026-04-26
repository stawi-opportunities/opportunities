package repository

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// MatchRepository wraps GORM operations for candidate job matches.
type MatchRepository struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

// NewMatchRepository creates a new MatchRepository.
func NewMatchRepository(db func(ctx context.Context, readOnly bool) *gorm.DB) *MatchRepository {
	return &MatchRepository{db: db}
}

// Upsert inserts or updates a single candidate match on conflict of
// (candidate_id, canonical_job_id).
func (r *MatchRepository) Upsert(ctx context.Context, m *domain.CandidateMatch) error {
	return r.db(ctx, false).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "candidate_id"},
				{Name: "canonical_job_id"},
			},
			DoUpdates: clause.AssignmentColumns([]string{
				"match_score", "skills_overlap", "embedding_similarity",
			}),
		}).
		Create(m).Error
}

// UpsertBatch batch-upserts candidate matches in groups of 100.
func (r *MatchRepository) UpsertBatch(ctx context.Context, matches []*domain.CandidateMatch) error {
	if len(matches) == 0 {
		return nil
	}
	return r.db(ctx, false).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "candidate_id"},
				{Name: "canonical_job_id"},
			},
			DoUpdates: clause.AssignmentColumns([]string{
				"match_score", "skills_overlap", "embedding_similarity",
			}),
		}).
		CreateInBatches(matches, 100).Error
}

// ListForCandidate returns all matches for a candidate ordered by match_score descending.
func (r *MatchRepository) ListForCandidate(ctx context.Context, candidateID string, limit int) ([]*domain.CandidateMatch, error) {
	var matches []*domain.CandidateMatch
	err := r.db(ctx, true).
		Where("candidate_id = ?", candidateID).
		Order("match_score DESC").
		Limit(limit).
		Find(&matches).Error
	return matches, err
}

// ListUnsent returns unsent (status='new') matches for a candidate.
func (r *MatchRepository) ListUnsent(ctx context.Context, candidateID string, limit int) ([]*domain.CandidateMatch, error) {
	var matches []*domain.CandidateMatch
	err := r.db(ctx, true).
		Where("candidate_id = ? AND status = ?", candidateID, domain.MatchNew).
		Order("match_score DESC").
		Limit(limit).
		Find(&matches).Error
	return matches, err
}

// MarkSent sets the match status to 'sent' and records the sent_at timestamp.
func (r *MatchRepository) MarkSent(ctx context.Context, id string) error {
	now := time.Now()
	return r.db(ctx, false).
		Model(&domain.CandidateMatch{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":  domain.MatchSent,
			"sent_at": now,
		}).Error
}

// MarkViewed sets the match status to 'viewed' and records the viewed_at timestamp.
func (r *MatchRepository) MarkViewed(ctx context.Context, id string) error {
	now := time.Now()
	return r.db(ctx, false).
		Model(&domain.CandidateMatch{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":    domain.MatchViewed,
			"viewed_at": now,
		}).Error
}

// CountForCandidate returns the total number of matches for a candidate.
func (r *MatchRepository) CountForCandidate(ctx context.Context, candidateID string) (int64, error) {
	var count int64
	err := r.db(ctx, true).
		Model(&domain.CandidateMatch{}).
		Where("candidate_id = ?", candidateID).
		Count(&count).Error
	return count, err
}
