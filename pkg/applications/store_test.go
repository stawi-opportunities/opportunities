//go:build integration

package applications_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/applications"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

func setupStoreDB(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	db := testhelpers.PostgresContainerNoMigrate(t, ctx)
	require.NoError(t, testhelpers.EnsureOpportunitiesStub(ctx, db))
	testhelpers.ApplyMigrationsDir(t, ctx, db, "../../db/migrations")
	return db, ctx
}

func TestStore_CreateAndGet(t *testing.T) {
	db, ctx := setupStoreDB(t)
	s := applications.NewStore(db)
	app, err := s.Create(ctx, applications.Application{
		ApplicationID: "a_1", CandidateID: "c1", OpportunityID: "o1", MatchID: "m1",
	})
	require.NoError(t, err)
	require.Equal(t, applications.StatusNew, app.Status)

	got, err := s.GetByID(ctx, "a_1")
	require.NoError(t, err)
	require.Equal(t, "c1", got.CandidateID)

	gotPair, err := s.GetByPair(ctx, "c1", "o1")
	require.NoError(t, err)
	require.Equal(t, "a_1", gotPair.ApplicationID)
}

func TestStore_CreateDuplicateReturnsAlreadyExists(t *testing.T) {
	db, ctx := setupStoreDB(t)
	s := applications.NewStore(db)
	_, _ = s.Create(ctx, applications.Application{
		ApplicationID: "a_2", CandidateID: "c", OpportunityID: "o", MatchID: "m",
	})
	_, err := s.Create(ctx, applications.Application{
		ApplicationID: "a_3", CandidateID: "c", OpportunityID: "o", MatchID: "m",
	})
	require.ErrorIs(t, err, applications.ErrAlreadyExists)
}

func TestStore_ApplyTransition_HappyPath(t *testing.T) {
	db, ctx := setupStoreDB(t)
	s := applications.NewStore(db)
	_, _ = s.Create(ctx, applications.Application{
		ApplicationID: "a_4", CandidateID: "c", OpportunityID: "o", MatchID: "m",
	})

	applying := applications.StatusApplying
	got, err := s.ApplyTransition(ctx, "a_4", applications.Patch{Status: &applying, LastEventID: "e1"})
	require.NoError(t, err)
	require.Equal(t, applying, got.Status)

	submitted := applications.StatusSubmitted
	got, err = s.ApplyTransition(ctx, "a_4", applications.Patch{Status: &submitted, LastEventID: "e2"})
	require.NoError(t, err)
	require.NotNil(t, got.SubmittedAt)
}

func TestStore_ApplyTransition_InvalidReturnsTypedError(t *testing.T) {
	db, ctx := setupStoreDB(t)
	s := applications.NewStore(db)
	_, _ = s.Create(ctx, applications.Application{
		ApplicationID: "a_5", CandidateID: "c", OpportunityID: "o", MatchID: "m",
	})
	offer := applications.StatusOffer
	_, err := s.ApplyTransition(ctx, "a_5", applications.Patch{Status: &offer})
	var ie *applications.InvalidTransitionError
	require.ErrorAs(t, err, &ie)
}

func TestStore_ListByCandidate_Pagination(t *testing.T) {
	db, ctx := setupStoreDB(t)
	s := applications.NewStore(db)
	for i := 0; i < 5; i++ {
		_, err := s.Create(ctx, applications.Application{
			ApplicationID: fmt.Sprintf("ap_%c", 'a'+i),
			CandidateID:   "u",
			OpportunityID: fmt.Sprintf("o%c", 'a'+i),
			MatchID:       fmt.Sprintf("m%c", 'a'+i),
		})
		require.NoError(t, err)
	}
	page, err := s.ListByCandidate(ctx, applications.ListByCandidateParams{CandidateID: "u", Limit: 3})
	require.NoError(t, err)
	require.Len(t, page.Items, 3)
	require.True(t, page.HasMore)
	page2, err := s.ListByCandidate(ctx, applications.ListByCandidateParams{CandidateID: "u", Limit: 3, Cursor: page.NextCursor})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(page2.Items), 1)
}
