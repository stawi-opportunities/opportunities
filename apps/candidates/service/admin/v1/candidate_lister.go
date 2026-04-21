package v1

import (
	"context"

	"stawi.jobs/pkg/domain"
)

// CandidateActiveRepo is the subset of repository.CandidateRepository
// the lister adapter needs. Kept narrow so tests can fake it without
// pulling the whole repo type.
type CandidateActiveRepo interface {
	ListActive(ctx context.Context, limit int) ([]*domain.CandidateProfile, error)
}

// RepoCandidateLister adapts a CandidateActiveRepo into the CandidateLister
// interface required by MatchesWeeklyHandler.
type RepoCandidateLister struct {
	repo  CandidateActiveRepo
	limit int
}

// NewRepoCandidateLister wires the adapter.
func NewRepoCandidateLister(repo CandidateActiveRepo, limit int) *RepoCandidateLister {
	if limit <= 0 {
		limit = 1000
	}
	return &RepoCandidateLister{repo: repo, limit: limit}
}

// ListActive returns the IDs of active candidates.
func (l *RepoCandidateLister) ListActive(ctx context.Context) ([]string, error) {
	rows, err := l.repo.ListActive(ctx, l.limit)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids, nil
}
