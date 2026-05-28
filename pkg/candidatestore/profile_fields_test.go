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

	// Create candidate_profiles table with full schema BEFORE running migrations.
	// In production this is created by the migrations Job running AutoMigrate;
	// in tests we create it directly so migrations can safely run their ALTER TABLE steps.
	_, err := rawDB.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS candidate_profiles (
			id VARCHAR(20) PRIMARY KEY,
			profile_id VARCHAR(255),
			status VARCHAR(20) NOT NULL DEFAULT 'unverified',
			subscription VARCHAR(20) NOT NULL DEFAULT 'free',
			auto_apply BOOLEAN NOT NULL DEFAULT false,
			cv_url TEXT,
			cv_storage_uri TEXT,
			cv_content_hash VARCHAR(64),
			current_title TEXT,
			seniority VARCHAR(30),
			years_experience INTEGER,
			skills text[],
			strong_skills text[],
			working_skills text[],
			tools_frameworks text[],
			certifications TEXT,
			preferred_roles TEXT,
			industries TEXT,
			education TEXT,
			preferred_locations TEXT,
			preferred_countries TEXT,
			remote_preference VARCHAR(20),
			salary_min REAL,
			salary_max REAL,
			currency VARCHAR(10),
			target_job_title TEXT,
			experience_level VARCHAR(30),
			job_search_status VARCHAR(30),
			preferred_regions TEXT,
			preferred_timezones TEXT,
			us_work_auth BOOLEAN,
			needs_sponsorship BOOLEAN,
			wants_ats_report BOOLEAN NOT NULL DEFAULT false,
			subscription_id VARCHAR(255),
			plan_id VARCHAR(100),
			languages TEXT,
			bio TEXT,
			work_history JSONB DEFAULT '[]'::jsonb,
			comm_email BOOLEAN NOT NULL DEFAULT true,
			comm_whatsapp BOOLEAN NOT NULL DEFAULT false,
			comm_telegram BOOLEAN NOT NULL DEFAULT false,
			comm_sms BOOLEAN NOT NULL DEFAULT false,
			embedding TEXT,
			matches_sent INTEGER NOT NULL DEFAULT 0,
			last_matched_at TIMESTAMPTZ,
			last_contacted_at TIMESTAMPTZ,
			cv_score INTEGER NOT NULL DEFAULT 0,
			cv_report_json JSONB DEFAULT '{}'::jsonb,
			cv_scored_at TIMESTAMPTZ,
			cv_scored_version VARCHAR(50),
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			deleted_at TIMESTAMPTZ,
			-- Legacy columns that migration 0003 will drop
			cv_raw BOOLEAN,
			cv_extracted TEXT,
			cv_embedding TEXT,
			cv_version VARCHAR(50),
			preferences JSONB,
			target_role TEXT,
			excluded_companies TEXT,
			score_components JSONB,
			priority_fixes TEXT
		)
	`)
	require.NoError(t, err)

	// Now run stub creation for opportunities and apply all migrations.
	// EnsureOpportunitiesStub will skip candidate_profiles (IF NOT EXISTS).
	require.NoError(t, testhelpers.EnsureOpportunitiesStub(ctx, rawDB))
	testhelpers.ApplyMigrationsDir(t, ctx, rawDB, "../../db/migrations")

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
