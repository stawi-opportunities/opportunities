package matching_test

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

func TestNewMetrics_ConstructsWithoutPanic(t *testing.T) {
	m := matching.NewMetrics(prometheus.NewRegistry())
	require.NotNil(t, m)
	require.NotNil(t, m.MatchesWritten)
	require.NotNil(t, m.MatchScore)
	require.NotNil(t, m.MatchLatency)
	require.NotNil(t, m.DLQDepth)
	require.NotNil(t, m.RerankerPoolInUse)
	require.NotNil(t, m.CandidateMatchIndexStale)

	// Increment one to verify the labels match.
	m.MatchesWritten.WithLabelValues("fanout", "job", "new").Inc()
}
