package matching_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

func TestMatchStatus_IsTerminal(t *testing.T) {
	require.True(t, matching.StatusDismissed.IsTerminal())
	require.True(t, matching.StatusApplied.IsTerminal())
	require.False(t, matching.StatusNew.IsTerminal())
	require.False(t, matching.StatusViewed.IsTerminal())
	require.False(t, matching.StatusApplying.IsTerminal())
	require.False(t, matching.StatusOverflow.IsTerminal())
}

func TestAllMatchStatuses(t *testing.T) {
	got := matching.AllStatuses()
	require.ElementsMatch(t, []matching.MatchStatus{
		matching.StatusNew,
		matching.StatusViewed,
		matching.StatusDismissed,
		matching.StatusApplying,
		matching.StatusApplied,
		matching.StatusOverflow,
	}, got)
}
