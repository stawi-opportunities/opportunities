//go:build integration

package candidatestore_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/candidatestore"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

func setupDB(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	rawDB := testhelpers.PostgresContainerNoMigrate(t, ctx)
	testhelpers.ApplyGreenfieldSchema(t, ctx, rawDB)
	return rawDB, ctx
}

func TestGetProfileFields_RoundTrip(t *testing.T) {
	db, ctx := setupDB(t)
	_, err := db.ExecContext(ctx, `
		INSERT INTO candidate_profiles
		    (id, current_title, target_job_title, years_experience,
		     skills, strong_skills, tools_frameworks,
		     preferred_locations, preferred_countries, remote_preference,
		     salary_min, salary_max, currency, work_history)
		VALUES ($1, 'Software Engineer', 'Senior Software Engineer', 5,
		        ARRAY['go','postgres','kubernetes']::text[],
		        ARRAY['go','postgres']::text[],
		        ARRAY['docker','grafana']::text[],
		        'Nairobi, Kampala', 'KE,UG', 'remote',
		        80000, 120000, 'USD',
		        '[{"company":"Acme","title":"SE","years":3}]'::jsonb)
	`, "cand_pf1")
	require.NoError(t, err)

	pf, etag, err := candidatestore.GetProfileFields(ctx, db, "cand_pf1")
	require.NoError(t, err)
	require.Equal(t, "cand_pf1", pf.CandidateID)
	require.Equal(t, "Software Engineer", pf.CurrentTitle)
	require.Equal(t, 5, pf.YearsExperience)
	require.ElementsMatch(t, []string{"go", "postgres", "kubernetes"}, pf.Skills)
	require.ElementsMatch(t, []string{"KE", "UG"}, pf.Countries)
	require.NotEmpty(t, pf.WorkHistory)
	require.Equal(t, "Acme", pf.WorkHistory[0]["company"])
	require.Contains(t, etag, `W/"`)
}

func TestGetProfileFields_NotFound(t *testing.T) {
	db, ctx := setupDB(t)
	_, _, err := candidatestore.GetProfileFields(ctx, db, "nope")
	require.ErrorIs(t, err, candidatestore.ErrProfileNotFound)
}
