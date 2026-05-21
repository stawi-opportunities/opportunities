package matching_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

func TestNewValkeyDebouncer_EmptyURLReturnsNil(t *testing.T) {
	d, err := matching.NewValkeyDebouncer("")
	require.NoError(t, err)
	require.Nil(t, d)
}

func TestNewValkeyDebouncer_BadURLReturnsError(t *testing.T) {
	_, err := matching.NewValkeyDebouncer("not-a-url")
	require.Error(t, err)
}

// Acquire behaviour is tested in integration tests against a live
// Valkey instance — skipped in CI for now since there's no shared
// fixture container in testhelpers. (Phase 6 follow-up.)
func TestValkeyDebouncer_AcquireBehaviour(t *testing.T) {
	t.Skip("requires live Valkey instance; covered by manual smoke tests until testhelpers provides a Valkey container")
	_ = context.Background()
	_ = 50 * time.Millisecond
}
