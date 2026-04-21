package v1

import (
	"context"
	"time"

	"stawi.jobs/pkg/candidatestore"
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

// StaleReaderIface is satisfied by *candidatestore.StaleReader and any
// test fake. Narrow interface for testability.
type StaleReaderIface interface {
	ListStale(ctx context.Context, cutoff time.Time, limit int) ([]candidatestore.StaleCandidate, error)
}

// R2StaleLister reads "most recent CV upload" timestamps from R2
// candidates_cv_current/ instead of Postgres' updated_at proxy.
type R2StaleLister struct {
	reader StaleReaderIface
	cutoff time.Duration
	limit  int
}

// NewR2StaleLister constructs an R2StaleLister. cutoff is the minimum
// age of the most-recent CV upload before the candidate is considered
// stale. limit caps the number of results returned.
func NewR2StaleLister(reader StaleReaderIface, cutoff time.Duration, limit int) *R2StaleLister {
	if cutoff <= 0 {
		cutoff = 60 * 24 * time.Hour
	}
	if limit <= 0 {
		limit = 500
	}
	return &R2StaleLister{reader: reader, cutoff: cutoff, limit: limit}
}

// ListStale delegates to the underlying StaleReaderIface, translating
// the asOf timestamp into a cutoff (asOf - l.cutoff) and mapping
// candidatestore.StaleCandidate to the local StaleCandidate shape.
func (l *R2StaleLister) ListStale(ctx context.Context, asOf time.Time) ([]StaleCandidate, error) {
	rows, err := l.reader.ListStale(ctx, asOf.Add(-l.cutoff), l.limit)
	if err != nil {
		return nil, err
	}
	out := make([]StaleCandidate, 0, len(rows))
	for _, r := range rows {
		out = append(out, StaleCandidate{CandidateID: r.CandidateID, LastUploadAt: r.LastUploadAt})
	}
	return out, nil
}
