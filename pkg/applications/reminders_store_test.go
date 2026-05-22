//go:build integration

package applications_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/applications"
)

func TestRemindersStore_LifeCycle(t *testing.T) {
	db, ctx := setupStoreDB(t)
	apps := applications.NewStore(db)
	_, err := apps.Create(ctx, applications.Application{
		ApplicationID: "ar1", CandidateID: "c", OpportunityID: "o", MatchID: "m",
	})
	require.NoError(t, err)

	rs := applications.NewRemindersStore(db)

	// Create
	r, err := rs.Create(ctx, applications.Reminder{
		ReminderID: "r1", ApplicationID: "ar1",
		DueAt: time.Now().Add(48 * time.Hour),
		Note:  "follow up after interview",
	})
	require.NoError(t, err)
	require.Equal(t, applications.ReminderPending, r.Status)
	require.Nil(t, r.CompletedAt)

	// Get
	got, err := rs.Get(ctx, "r1")
	require.NoError(t, err)
	require.Equal(t, "r1", got.ReminderID)

	// ListActiveByApplication shows it
	list, err := rs.ListActiveByApplication(ctx, "ar1")
	require.NoError(t, err)
	require.Len(t, list, 1)

	// Transition to done — sets completed_at
	done := applications.ReminderDone
	r2, err := rs.Update(ctx, "r1", applications.ReminderPatch{Status: &done})
	require.NoError(t, err)
	require.Equal(t, applications.ReminderDone, r2.Status)
	require.NotNil(t, r2.CompletedAt)

	// SoftDelete
	require.NoError(t, rs.SoftDelete(ctx, "r1"))

	// ListActiveByApplication no longer shows it
	list, err = rs.ListActiveByApplication(ctx, "ar1")
	require.NoError(t, err)
	require.Empty(t, list)
}

func TestRemindersStore_UpdateDueAtAndNote(t *testing.T) {
	db, ctx := setupStoreDB(t)
	apps := applications.NewStore(db)
	_, err := apps.Create(ctx, applications.Application{
		ApplicationID: "ar2", CandidateID: "c2", OpportunityID: "o2", MatchID: "m2",
	})
	require.NoError(t, err)

	rs := applications.NewRemindersStore(db)
	_, err = rs.Create(ctx, applications.Reminder{
		ReminderID: "r2", ApplicationID: "ar2",
		DueAt: time.Now().Add(24 * time.Hour),
	})
	require.NoError(t, err)

	newDue := time.Now().Add(72 * time.Hour)
	newNote := "rescheduled"
	updated, err := rs.Update(ctx, "r2", applications.ReminderPatch{
		DueAt: &newDue,
		Note:  &newNote,
	})
	require.NoError(t, err)
	require.Equal(t, "rescheduled", updated.Note)
	require.Equal(t, applications.ReminderPending, updated.Status)
}

func TestRemindersStore_GetMissingReturnsNotFound(t *testing.T) {
	db, ctx := setupStoreDB(t)
	rs := applications.NewRemindersStore(db)
	_, err := rs.Get(ctx, "nonexistent")
	require.ErrorIs(t, err, applications.ErrNotFound)
}
