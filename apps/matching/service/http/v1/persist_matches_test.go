package v1

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/billing"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

type memMatchStore struct {
	ms []matching.Match
}

func (m *memMatchStore) UpsertMatches(_ context.Context, ms []matching.Match) error {
	m.ms = append(m.ms, ms...)
	return nil
}

func TestPersistMatchResult_WeeklyCapOverflow(t *testing.T) {
	t.Parallel()
	store := &memMatchStore{}
	res := MatchResult{
		CandidateID: "c1",
		Matches: []SearchHit{
			{CanonicalID: "o1", Score: 0.9},
			{CanonicalID: "o2", Score: 0.8},
			{CanonicalID: "o3", Score: 0.7},
		},
	}
	caps := CapsFromEntitlements(billing.EntitlementsFor(""), 2, 0) // free weekly 3, already 2 used → 1 new
	require.NoError(t, PersistMatchResult(context.Background(), store, res, "test", &caps))
	require.Len(t, store.ms, 3)
	// Highest score first should be new; rest overflow.
	require.Equal(t, matching.StatusNew, store.ms[0].Status)
	require.Equal(t, "o1", store.ms[0].OpportunityID)
	require.Equal(t, matching.StatusOverflow, store.ms[1].Status)
	require.Equal(t, matching.StatusOverflow, store.ms[2].Status)
}

func TestPersistMatchResult_UncappedWeekly(t *testing.T) {
	t.Parallel()
	store := &memMatchStore{}
	res := MatchResult{
		CandidateID: "c1",
		Matches: []SearchHit{
			{CanonicalID: "o1", Score: 0.5},
			{CanonicalID: "o2", Score: 0.4},
		},
	}
	caps := CapsFromEntitlements(billing.EntitlementsFor(billing.PlanManaged), 0, 0)
	require.NoError(t, PersistMatchResult(context.Background(), store, res, "test", &caps))
	require.Equal(t, matching.StatusNew, store.ms[0].Status)
	require.Equal(t, matching.StatusNew, store.ms[1].Status)
}

func TestEntitlementsForProfile_FreeDespitePlanID(t *testing.T) {
	t.Parallel()
	ent := billing.EntitlementsForProfile("free", "starter")
	require.Equal(t, 1, ent.DailyCap)
	require.Equal(t, 3, ent.WeeklyCap)
	ent2 := billing.EntitlementsForProfile("paid", "starter")
	require.Equal(t, 2, ent2.DailyCap)
	require.Equal(t, 5, ent2.WeeklyCap)
}
