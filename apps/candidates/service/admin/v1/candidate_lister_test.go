package v1

import (
	"context"
	"testing"

	"stawi.jobs/pkg/domain"
)

// fakeCandidateRepo implements just ListActive.
type fakeCandidateRepo struct {
	rows []*domain.CandidateProfile
}

func (r *fakeCandidateRepo) ListActive(_ context.Context, _ int) ([]*domain.CandidateProfile, error) {
	return r.rows, nil
}

func TestRepoCandidateListerReturnsIDs(t *testing.T) {
	rows := []*domain.CandidateProfile{
		{BaseModel: domain.BaseModel{ID: "cnd_1"}},
		{BaseModel: domain.BaseModel{ID: "cnd_2"}},
	}
	lister := NewRepoCandidateLister(&fakeCandidateRepo{rows: rows}, 500)
	ids, err := lister.ListActive(context.Background())
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(ids) != 2 || ids[0] != "cnd_1" || ids[1] != "cnd_2" {
		t.Fatalf("ids=%v", ids)
	}
}
