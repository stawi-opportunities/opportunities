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
	// Explicit legacy row caps (product entitlements now set WeeklyCap=0).
	// weekly 3, already 2 used → 1 new.
	caps := PersistCaps{WeeklyCap: 3, WeekUsed: 2}
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
	// Product path: all plans set DailyCap/WeeklyCap to 0 (unlimited rows).
	caps := CapsFromEntitlements(billing.EntitlementsFor(billing.PlanManaged), 0, 0)
	require.Equal(t, 0, caps.WeeklyCap)
	require.NoError(t, PersistMatchResult(context.Background(), store, res, "test", &caps))
	require.Equal(t, matching.StatusNew, store.ms[0].Status)
	require.Equal(t, matching.StatusNew, store.ms[1].Status)
}

func TestEntitlementsForProfile_FreeDespitePlanID(t *testing.T) {
	t.Parallel()
	ent := billing.EntitlementsForProfile("free", "starter")
	require.Equal(t, 0, ent.DailyCap)
	require.Equal(t, 0, ent.WeeklyCap)
	require.Equal(t, 1, ent.InvokeDailyLimit)
	ent2 := billing.EntitlementsForProfile("paid", "starter")
	require.Equal(t, 0, ent2.DailyCap)
	require.Equal(t, 0, ent2.WeeklyCap)
	require.Equal(t, 30, ent2.InvokeDailyLimit)
}
