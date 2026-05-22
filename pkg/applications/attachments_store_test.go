//go:build integration

package applications_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/applications"
)

func TestAttachmentsStore_CreateGetSoftDelete(t *testing.T) {
	db, ctx := setupStoreDB(t)
	apps := applications.NewStore(db)
	_, err := apps.Create(ctx, applications.Application{
		ApplicationID: "aa1", CandidateID: "c", OpportunityID: "o", MatchID: "m",
	})
	require.NoError(t, err)

	as := applications.NewAttachmentsStore(db)

	// Create
	a, err := as.Create(ctx, applications.Attachment{
		AttachmentID: "at1", ApplicationID: "aa1",
		R2Key:       "applications/c/aa1/cv.pdf",
		ContentType: "application/pdf", Bytes: 12345, Filename: "cv.pdf",
	})
	require.NoError(t, err)
	require.Equal(t, "cv.pdf", a.Filename)
	require.Equal(t, int64(12345), a.Bytes)
	require.Nil(t, a.DeletedAt)

	// Get
	got, err := as.Get(ctx, "at1")
	require.NoError(t, err)
	require.Equal(t, "at1", got.AttachmentID)
	require.Equal(t, "applications/c/aa1/cv.pdf", got.R2Key)

	// ListByApplication shows it
	list, err := as.ListByApplication(ctx, "aa1")
	require.NoError(t, err)
	require.Len(t, list, 1)

	// SoftDelete
	require.NoError(t, as.SoftDelete(ctx, "at1"))

	// ListByApplication no longer shows it
	list, err = as.ListByApplication(ctx, "aa1")
	require.NoError(t, err)
	require.Empty(t, list)

	// Get still works (returns deleted row)
	deleted, err := as.Get(ctx, "at1")
	require.NoError(t, err)
	require.NotNil(t, deleted.DeletedAt)
}

func TestAttachmentsStore_NoFilenameAllowed(t *testing.T) {
	db, ctx := setupStoreDB(t)
	apps := applications.NewStore(db)
	_, err := apps.Create(ctx, applications.Application{
		ApplicationID: "aa2", CandidateID: "c2", OpportunityID: "o2", MatchID: "m2",
	})
	require.NoError(t, err)

	as := applications.NewAttachmentsStore(db)
	a, err := as.Create(ctx, applications.Attachment{
		AttachmentID: "at2", ApplicationID: "aa2",
		R2Key: "k", ContentType: "image/png", Bytes: 1,
		// Filename intentionally empty
	})
	require.NoError(t, err)
	require.Equal(t, "", a.Filename)
}

func TestAttachmentsStore_GetMissingReturnsNotFound(t *testing.T) {
	db, ctx := setupStoreDB(t)
	as := applications.NewAttachmentsStore(db)
	_, err := as.Get(ctx, "nonexistent")
	require.ErrorIs(t, err, applications.ErrNotFound)
}
