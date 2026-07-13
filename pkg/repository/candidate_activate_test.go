//go:build integration

package repository_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
)

// TestActivateSubscription_FlipsFreeToPaidIdempotently proves the core
// activation path WITHOUT a live payment: a seeded candidate row with
// subscription='free' is flipped to 'paid' with subscription_id + plan_id
// set, and a second identical call is a no-op (changed=false).
func TestActivateSubscription_FlipsFreeToPaidIdempotently(t *testing.T) {
	g, sqlDB, ctx := setupCandidateDB(t)

	// The stub candidate_profiles table only has `id`; add the columns the
	// GORM model touches (including BaseModel.deleted_at, which GORM's
	// soft-delete clause references). Production gets these via AutoMigrate.
	_, err := sqlDB.ExecContext(ctx, `
		ALTER TABLE candidate_profiles
			ADD COLUMN IF NOT EXISTS subscription    TEXT NOT NULL DEFAULT 'free',
			ADD COLUMN IF NOT EXISTS subscription_id TEXT NOT NULL DEFAULT '',
			ADD COLUMN IF NOT EXISTS plan_id         TEXT NOT NULL DEFAULT '',
			ADD COLUMN IF NOT EXISTS auto_apply      BOOLEAN NOT NULL DEFAULT false,
			ADD COLUMN IF NOT EXISTS updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
			ADD COLUMN IF NOT EXISTS deleted_at      TIMESTAMPTZ`)
	require.NoError(t, err)

	_, err = sqlDB.ExecContext(ctx,
		`INSERT INTO candidate_profiles (id, subscription, plan_id) VALUES ($1, 'free', 'pro')`,
		"cand_pay_1")
	require.NoError(t, err)

	repo := repository.NewCandidateRepository(func(_ context.Context, _ bool) *gorm.DB { return g })

	changed, err := repo.ActivateSubscription(ctx, "cand_pay_1", "sub_abc", "pro")
	require.NoError(t, err)
	require.True(t, changed, "first activation should change the row")

	var sub, subID, plan string
	require.NoError(t, sqlDB.QueryRowContext(ctx,
		`SELECT subscription, subscription_id, plan_id FROM candidate_profiles WHERE id=$1`, "cand_pay_1").
		Scan(&sub, &subID, &plan))
	require.Equal(t, string(domain.SubscriptionPaid), sub)
	require.Equal(t, "sub_abc", subID)
	require.Equal(t, "pro", plan)

	// Idempotent: re-activating the same (candidate, subscription) is a no-op.
	changed, err = repo.ActivateSubscription(ctx, "cand_pay_1", "sub_abc", "pro")
	require.NoError(t, err)
	require.False(t, changed, "second identical activation should not change the row")
}
