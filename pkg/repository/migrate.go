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
// crawl_jobs + raw_payloads are TimescaleDB hypertables (promoted in
// place by 20260610_0130_timescale_append_only.sql, which also enforces
// append-only on the pure event ledgers at the database level).
//
// pool.Migrate AutoMigrates the models and applies the SQL files under
// migrations/0001 — including the Postgres-specific bits GORM can't express
// (pg_trgm extension, the partial unique index on source_recipes), which live
// as migration files rather than a Go helper.
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
		&domain.Company{},
		&domain.CrawlRun{},
	); err != nil {
		return fmt.Errorf("pool migrate: %w", err)
	}

	return nil
}
