package v1

import (
	"context"
	"time"

	"stawi.jobs/pkg/domain"
)

// InactiveRepo is the narrow interface the stale lister depends on.
// Satisfied by *repository.CandidateRepository after Task 17 adds the
// ListInactiveSince method.
type InactiveRepo interface {
	ListInactiveSince(ctx context.Context, cutoff time.Time, limit int) ([]*domain.CandidateProfile, error)
}

// RepoStaleLister adapts an InactiveRepo to the StaleLister interface
// expected by CVStaleNudgeHandler.
type RepoStaleLister struct {
	repo  InactiveRepo
	limit int
}

// NewRepoStaleLister wires the adapter.
func NewRepoStaleLister(repo InactiveRepo, limit int) *RepoStaleLister {
	if limit <= 0 {
		limit = 1000
	}
	return &RepoStaleLister{repo: repo, limit: limit}
}

// ListStale maps repository rows to the StaleCandidate shape used by
// CVStaleNudgeHandler. `LastUploadAt` is filled from `updated_at` as a
// v1 proxy for upload time; Phase 6 replaces this with a real last-
// upload timestamp read from R2 Parquet.
func (l *RepoStaleLister) ListStale(ctx context.Context, asOf time.Time) ([]StaleCandidate, error) {
	rows, err := l.repo.ListInactiveSince(ctx, asOf, l.limit)
	if err != nil {
		return nil, err
	}
	out := make([]StaleCandidate, 0, len(rows))
	for _, r := range rows {
		out = append(out, StaleCandidate{
			CandidateID:  r.ID,
			LastUploadAt: r.UpdatedAt,
		})
	}
	return out, nil
}
