//go:build integration

package matching_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/applications"
	"github.com/stawi-opportunities/opportunities/pkg/billing"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
	"github.com/stawi-opportunities/opportunities/pkg/savedjobs"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

func TestCandidateStateUsesGORMWithVectorCapabilities(t *testing.T) {
	ctx := context.Background()
	db := testhelpers.PostgresContainerNoMigrate(t, ctx)
	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	require.NoError(t, err)
	models := matching.Schema()
	models = append(models, applications.Schema()...)
	models = append(models, savedjobs.Schema()...)
	models = append(models, billing.Schema()...)
	require.NoError(t, gormDB.AutoMigrate(models...))

	sql, err := os.ReadFile("../../apps/matching/migrations/0001/20260706_0017_postgres_candidate_state.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(sql))
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `INSERT INTO candidate_match_indexes
		(candidate_id,embedding) VALUES ('candidate-1', array_fill(0::real, ARRAY[1024])::vector)`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO candidate_match_indexes (candidate_id) VALUES ('candidate-2')`)
	require.Error(t, err, "embedding must remain required")

	_, err = db.ExecContext(ctx, `INSERT INTO applications
		(application_id,candidate_id,opportunity_id,match_id,status)
		VALUES ('app-1','candidate-1','opportunity-1','match-1','new')`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO candidate_saved_jobs
		(candidate_id,opportunity_id) VALUES ('candidate-1','opportunity-1')`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO candidate_checkouts
		(prompt_id,candidate_id,plan_id) VALUES ('prompt-1','candidate-1','pro')`)
	require.NoError(t, err)
}
