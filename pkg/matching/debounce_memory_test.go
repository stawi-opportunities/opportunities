package matching_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

func TestMemoryDebouncer_FirstAllowedSecondDenied(t *testing.T) {
	d := matching.NewMemoryDebouncer()
	ok, err := d.Acquire(context.Background(), "u", 100*time.Millisecond)
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = d.Acquire(context.Background(), "u", 100*time.Millisecond)
	require.NoError(t, err)
	require.False(t, ok)

	time.Sleep(110 * time.Millisecond)
	ok, _ = d.Acquire(context.Background(), "u", 100*time.Millisecond)
	require.True(t, ok)
}
