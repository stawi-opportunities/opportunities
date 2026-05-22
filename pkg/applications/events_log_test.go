//go:build integration

package applications_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/applications"
)

func TestEventLog_WriteAndList(t *testing.T) {
	db, ctx := setupStoreDB(t)
	el := applications.NewEventLog(db)
	require.NoError(t, el.Write(ctx, applications.Event{
		EventID: "e_1", ApplicationID: "a_e1", CandidateID: "c1",
		Kind:       applications.EventKindCreated,
		Actor:      applications.ActorExtension,
		Data:       map[string]any{"src": "ext"},
		OccurredAt: time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
	}))
	require.NoError(t, el.Write(ctx, applications.Event{
		EventID: "e_2", ApplicationID: "a_e1", CandidateID: "c1",
		Kind:       applications.EventKindStateChanged,
		FromStatus: applications.StatusNew,
		ToStatus:   applications.StatusApplying,
		Actor:      applications.ActorExtension,
		OccurredAt: time.Date(2026, 5, 21, 12, 1, 0, 0, time.UTC),
	}))
	evts, err := el.ListByApplication(ctx, "a_e1", 10)
	require.NoError(t, err)
	require.Len(t, evts, 2)
	require.Equal(t, applications.EventKindStateChanged, evts[0].Kind) // newest first
}
