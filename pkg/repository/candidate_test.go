//go:build integration

package repository_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/repository"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

func setupCandidateDB(t *testing.T) (*gorm.DB, *sql.DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	sqlDB := testhelpers.PostgresContainerNoMigrate(t, ctx)
	require.NoError(t, testhelpers.EnsureOpportunitiesStub(ctx, sqlDB))
	testhelpers.ApplyMigrationsDir(t, ctx, sqlDB, "../../db/migrations")
	g, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)
	return g, sqlDB, ctx
}

// insertCandidate inserts a minimal candidate row using raw SQL. The
// candidate_profiles table is created by the EnsureOpportunitiesStub
// helper with only an `id` primary key; additional columns are added
// by GORM AutoMigrate in production. For test purposes we only need
// the row to exist so the draft methods have something to operate on.
func insertCandidate(t *testing.T, ctx context.Context, db *sql.DB, id string) {
	t.Helper()
	_, err := db.ExecContext(ctx,
		`INSERT INTO candidate_profiles (id) VALUES ($1)`,
		id,
	)
	require.NoError(t, err)
}

func TestCandidateRepository_OnboardingDraft_RoundTrip(t *testing.T) {
	g, sqlDB, ctx := setupCandidateDB(t)
	repo := repository.NewCandidateRepository(func(_ context.Context, _ bool) *gorm.DB { return g })

	insertCandidate(t, ctx, sqlDB, "cand_draft_a")

	// Fresh candidate has an empty draft.
	got, err := repo.GetOnboardingDraft(ctx, "cand_draft_a")
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(got))

	// Write a draft, read it back identically.
	draft := json.RawMessage(`{"step":2,"fields":{"target_job_title":"Backend Engineer"}}`)
	require.NoError(t, repo.SetOnboardingDraft(ctx, "cand_draft_a", draft))
	got, err = repo.GetOnboardingDraft(ctx, "cand_draft_a")
	require.NoError(t, err)
	require.JSONEq(t, string(draft), string(got))

	// Clear resets to {}.
	require.NoError(t, repo.ClearOnboardingDraft(ctx, "cand_draft_a"))
	got, err = repo.GetOnboardingDraft(ctx, "cand_draft_a")
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(got))
}

func TestCandidateRepository_OnboardingDraft_UnknownCandidateReturnsEmpty(t *testing.T) {
	g, _, ctx := setupCandidateDB(t)
	repo := repository.NewCandidateRepository(func(_ context.Context, _ bool) *gorm.DB { return g })

	got, err := repo.GetOnboardingDraft(ctx, "cand_nonexistent")
	require.NoError(t, err, "GetOnboardingDraft must not 500 on unknown candidate; the wizard treats that as 'fresh user'")
	require.JSONEq(t, `{}`, string(got))
}

func TestCandidateRepository_OnboardingDraft_LazyCreate(t *testing.T) {
	g, _, ctx := setupCandidateDB(t)
	repo := repository.NewCandidateRepository(func(_ context.Context, _ bool) *gorm.DB { return g })

	// SetOnboardingDraft on a non-existent candidate must lazy-create the row
	// and store the draft rather than silently dropping it.
	draft := json.RawMessage(`{"step":1,"fields":{"target_job_title":"DevOps Engineer"}}`)
	require.NoError(t, repo.SetOnboardingDraft(ctx, "cand_lazy", draft))

	got, err := repo.GetOnboardingDraft(ctx, "cand_lazy")
	require.NoError(t, err)
	require.JSONEq(t, string(draft), string(got))
}

func TestCandidateRepository_Transaction_DraftClearedOnCommit(t *testing.T) {
	g, sqlDB, ctx := setupCandidateDB(t)
	repo := repository.NewCandidateRepository(func(_ context.Context, _ bool) *gorm.DB { return g })

	insertCandidate(t, ctx, sqlDB, "cand_txn")
	require.NoError(t, repo.SetOnboardingDraft(ctx, "cand_txn", json.RawMessage(`{"step":3,"fields":{"plan":"pro"}}`)))

	err := repo.Transaction(ctx, func(tx *repository.CandidateRepository) error {
		return tx.ClearOnboardingDraft(ctx, "cand_txn")
	})
	require.NoError(t, err)

	got, err := repo.GetOnboardingDraft(ctx, "cand_txn")
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(got))
}

func TestCandidateRepository_Transaction_DraftSurvivesOnRollback(t *testing.T) {
	g, sqlDB, ctx := setupCandidateDB(t)
	repo := repository.NewCandidateRepository(func(_ context.Context, _ bool) *gorm.DB { return g })

	insertCandidate(t, ctx, sqlDB, "cand_txn2")
	require.NoError(t, repo.SetOnboardingDraft(ctx, "cand_txn2", json.RawMessage(`{"step":2,"fields":{"target_job_title":"PM"}}`)))

	_ = repo.Transaction(ctx, func(tx *repository.CandidateRepository) error {
		require.NoError(t, tx.ClearOnboardingDraft(ctx, "cand_txn2"))
		return errors.New("force rollback")
	})

	got, err := repo.GetOnboardingDraft(ctx, "cand_txn2")
	require.NoError(t, err)
	require.JSONEq(t, `{"step":2,"fields":{"target_job_title":"PM"}}`, string(got))
}
