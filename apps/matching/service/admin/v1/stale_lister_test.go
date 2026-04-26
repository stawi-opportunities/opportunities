package v1

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/candidatestore"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

type fakeInactiveRepo struct {
	cutoff time.Time
	rows   []*domain.CandidateProfile
}

func (r *fakeInactiveRepo) ListInactiveSince(_ context.Context, cutoff time.Time, _ int) ([]*domain.CandidateProfile, error) {
	r.cutoff = cutoff
	return r.rows, nil
}

type fakeR2Reader struct{ fixed []candidatestore.StaleCandidate }

func (f fakeR2Reader) ListStale(_ context.Context, _ time.Time, _ int) ([]candidatestore.StaleCandidate, error) {
	return f.fixed, nil
}

func TestR2StaleLister_AppliesCutoffOffset(t *testing.T) {
	now := time.Now().UTC()
	fake := fakeR2Reader{fixed: []candidatestore.StaleCandidate{
		{CandidateID: "c1", LastUploadAt: now.Add(-90 * 24 * time.Hour)},
	}}
	l := NewR2StaleLister(fake, 60*24*time.Hour, 100)
	got, err := l.ListStale(context.Background(), now)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "c1", got[0].CandidateID)
}

func TestRepoStaleListerMapsRowsToStaleCandidates(t *testing.T) {
	updated := time.Now().UTC().Add(-75 * 24 * time.Hour)
	repo := &fakeInactiveRepo{rows: []*domain.CandidateProfile{
		{BaseModel: domain.BaseModel{ID: "cnd_a", UpdatedAt: updated}},
		{BaseModel: domain.BaseModel{ID: "cnd_b", UpdatedAt: updated}},
	}}
	lister := NewRepoStaleLister(repo, 500)
	stale, err := lister.ListStale(context.Background(), time.Now().UTC().Add(-60*24*time.Hour))
	if err != nil {
		t.Fatalf("ListStale: %v", err)
	}
	if len(stale) != 2 || stale[0].CandidateID != "cnd_a" {
		t.Fatalf("bad result: %+v", stale)
	}
	if repo.cutoff.IsZero() {
		t.Fatalf("cutoff not forwarded to repo")
	}
}
