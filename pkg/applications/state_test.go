package applications_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/applications"
)

func TestState_AllStatesEnumerated(t *testing.T) {
	all := applications.AllStatuses()
	want := []applications.Status{
		applications.StatusNew,
		applications.StatusDismissed,
		applications.StatusApplying,
		applications.StatusSubmitted,
		applications.StatusScreening,
		applications.StatusInterview,
		applications.StatusOffer,
		applications.StatusAccepted,
		applications.StatusRejected,
		applications.StatusWithdrawn,
	}
	require.ElementsMatch(t, want, all)
}

func TestState_TerminalStatuses(t *testing.T) {
	for _, s := range []applications.Status{
		applications.StatusDismissed,
		applications.StatusAccepted,
		applications.StatusRejected,
		applications.StatusWithdrawn,
	} {
		require.True(t, s.IsTerminal(), "%s should be terminal", s)
	}
	for _, s := range []applications.Status{
		applications.StatusNew,
		applications.StatusApplying,
		applications.StatusSubmitted,
		applications.StatusScreening,
		applications.StatusInterview,
		applications.StatusOffer,
	} {
		require.False(t, s.IsTerminal(), "%s should not be terminal", s)
	}
}

func TestState_HappyPath(t *testing.T) {
	path := []applications.Status{
		applications.StatusNew,
		applications.StatusApplying,
		applications.StatusSubmitted,
		applications.StatusScreening,
		applications.StatusInterview,
		applications.StatusOffer,
		applications.StatusAccepted,
	}
	for i := 0; i < len(path)-1; i++ {
		require.NoError(t, applications.ValidateTransition(path[i], path[i+1]),
			"transition %s → %s should be allowed", path[i], path[i+1])
	}
}

func TestState_InvalidTransitions(t *testing.T) {
	cases := []struct {
		from, to applications.Status
	}{
		{applications.StatusNew, applications.StatusSubmitted},        // skip applying
		{applications.StatusNew, applications.StatusOffer},             // skip everything
		{applications.StatusAccepted, applications.StatusApplying},     // terminal → anything
		{applications.StatusRejected, applications.StatusApplying},     // terminal
		{applications.StatusDismissed, applications.StatusApplying},    // terminal
		{applications.StatusSubmitted, applications.StatusNew},         // can't go backwards
		{applications.StatusInterview, applications.StatusApplying},    // backwards
	}
	for _, tc := range cases {
		t.Run(string(tc.from)+"_to_"+string(tc.to), func(t *testing.T) {
			err := applications.ValidateTransition(tc.from, tc.to)
			require.Error(t, err)
			var ie *applications.InvalidTransitionError
			require.ErrorAs(t, err, &ie)
			require.Equal(t, tc.from, ie.From)
			require.Equal(t, tc.to, ie.To)
		})
	}
}

func TestState_DropToRejectedOrWithdrawnFromAnyNonTerminal(t *testing.T) {
	for _, from := range []applications.Status{
		applications.StatusApplying,
		applications.StatusSubmitted,
		applications.StatusScreening,
		applications.StatusInterview,
		applications.StatusOffer,
	} {
		for _, to := range []applications.Status{
			applications.StatusRejected,
			applications.StatusWithdrawn,
		} {
			require.NoError(t, applications.ValidateTransition(from, to),
				"%s → %s should be allowed", from, to)
		}
	}
}

func TestState_NewCanGoToDismissedOrApplying(t *testing.T) {
	require.NoError(t, applications.ValidateTransition(
		applications.StatusNew, applications.StatusDismissed))
	require.NoError(t, applications.ValidateTransition(
		applications.StatusNew, applications.StatusApplying))
}

func TestState_AllowedNext(t *testing.T) {
	got := applications.AllowedNext(applications.StatusInterview)
	require.ElementsMatch(t, []applications.Status{
		applications.StatusOffer,
		applications.StatusRejected,
		applications.StatusWithdrawn,
	}, got)
}

func TestState_UnknownStatus(t *testing.T) {
	err := applications.ValidateTransition(applications.Status("bogus"), applications.StatusApplying)
	require.Error(t, err)
}
