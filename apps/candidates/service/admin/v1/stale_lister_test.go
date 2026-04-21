package v1

import (
	"context"
	"testing"
	"time"

	"stawi.jobs/pkg/domain"
)

type fakeInactiveRepo struct {
	cutoff time.Time
	rows   []*domain.CandidateProfile
}

func (r *fakeInactiveRepo) ListInactiveSince(_ context.Context, cutoff time.Time, _ int) ([]*domain.CandidateProfile, error) {
	r.cutoff = cutoff
	return r.rows, nil
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
