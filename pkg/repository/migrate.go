package repository

import (
	"context"
	"fmt"

	"github.com/pitabwire/frame/datastore"
	"github.com/pitabwire/frame/datastore/pool"
	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// Migrate applies SQL migrations from migrationsDirPath and runs
// GORM AutoMigrate for the tables the SQL doesn't own (sources +
// raw_refs, historically GORM-managed, plus source_recipes — a
// crawler-owned table whose lifecycle the model drives directly).
//
// The hypertable (pipeline_variants) and the canonical opportunities
// table are SQL-owned via the timestamped *.sql files in
// migrationsDirPath. AutoMigrate would fight the composite PK +
// create_hypertable promotion on pipeline_variants, so it's
// explicitly excluded.
//
// The crawl_jobs + raw_payloads tables are currently plain GORM-
// managed (the hypertable promotion is a deferred follow-up); they
// are migrated via AutoMigrate on the matching service. This crawler
// migration path only owns the SQL-only schema deltas.
//
// FinalizeSchema runs after pool.Migrate to apply Postgres-specific
// extras (pg_trgm extension, the partial unique index on
// source_recipes) that GORM AutoMigrate can't express.
func Migrate(ctx context.Context, dbManager datastore.Manager, migrationsDirPath string) error {
	dbPool := dbManager.GetPool(ctx, datastore.DefaultPoolName)
	return migratePool(ctx, dbPool, migrationsDirPath)
}

func migratePool(ctx context.Context, dbPool pool.Pool, migrationsDirPath string) error {
	log := util.Log(ctx)
	log.WithField("path", migrationsDirPath).Info("running database migrations")

	if err := dbPool.Migrate(ctx, migrationsDirPath,
		&domain.Source{},
		&domain.RawRef{},
		&SourceRecipe{},
	); err != nil {
		return fmt.Errorf("pool migrate: %w", err)
	}

	// Postgres-specific schema bits GORM AutoMigrate can't express
	// (e.g. pg_trgm extension). Idempotent and safe to re-run.
	if err := FinalizeSchema(dbPool.DB(ctx, false)); err != nil {
		return fmt.Errorf("finalize schema: %w", err)
	}

	return nil
}
