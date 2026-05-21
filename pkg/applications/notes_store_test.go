//go:build integration

package applications_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/applications"
)

func TestNotesStore_CreateGetUpdateSoftDelete(t *testing.T) {
	db, ctx := setupStoreDB(t)
	// notes reference applications via FK — create the parent row first.
	apps := applications.NewStore(db)
	_, err := apps.Create(ctx, applications.Application{
		ApplicationID: "an1", CandidateID: "c", OpportunityID: "o", MatchID: "m",
	})
	require.NoError(t, err)

	ns := applications.NewNotesStore(db)

	// Create
	n, err := ns.Create(ctx, applications.Note{NoteID: "n1", ApplicationID: "an1", Body: "first"})
	require.NoError(t, err)
	require.Equal(t, "first", n.Body)
	require.Equal(t, "an1", n.ApplicationID)
	require.Nil(t, n.DeletedAt)

	// Get
	got, err := ns.Get(ctx, "n1")
	require.NoError(t, err)
	require.Equal(t, "n1", got.NoteID)

	// Update
	n2, err := ns.Update(ctx, "n1", "second")
	require.NoError(t, err)
	require.Equal(t, "second", n2.Body)

	// ListByApplication shows it
	list, err := ns.ListByApplication(ctx, "an1", 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "second", list[0].Body)

	// SoftDelete
	require.NoError(t, ns.SoftDelete(ctx, "n1"))

	// ListByApplication no longer shows it
	list, err = ns.ListByApplication(ctx, "an1", 10)
	require.NoError(t, err)
	require.Empty(t, list)

	// Get still works (returns deleted row)
	deleted, err := ns.Get(ctx, "n1")
	require.NoError(t, err)
	require.NotNil(t, deleted.DeletedAt)
}

func TestNotesStore_UpdateDeletedReturnsNotFound(t *testing.T) {
	db, ctx := setupStoreDB(t)
	apps := applications.NewStore(db)
	_, err := apps.Create(ctx, applications.Application{
		ApplicationID: "an2", CandidateID: "c2", OpportunityID: "o2", MatchID: "m2",
	})
	require.NoError(t, err)

	ns := applications.NewNotesStore(db)
	_, err = ns.Create(ctx, applications.Note{NoteID: "n2", ApplicationID: "an2", Body: "body"})
	require.NoError(t, err)
	require.NoError(t, ns.SoftDelete(ctx, "n2"))

	_, err = ns.Update(ctx, "n2", "new body")
	require.ErrorIs(t, err, applications.ErrNotFound)
}

func TestNotesStore_GetMissingReturnsNotFound(t *testing.T) {
	db, ctx := setupStoreDB(t)
	ns := applications.NewNotesStore(db)
	_, err := ns.Get(ctx, "nonexistent")
	require.ErrorIs(t, err, applications.ErrNotFound)
}
