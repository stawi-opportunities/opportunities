package repository

import (
	"context"
	"fmt"

	"github.com/pitabwire/frame/v2/datastore"
	"github.com/pitabwire/frame/v2/datastore/pool"
	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/frontier"
	"github.com/stawi-opportunities/opportunities/pkg/jobqueue"
)

// Migrate AutoMigrates ordinary PostgreSQL table shape, then applies the
// capability SQL from migrationsDirPath.
//
// crawl_jobs is a TimescaleDB hypertable. Immutable operational events use
// append-only hypertables; parsed current state uses ordinary PostgreSQL.
//
// pool.Migrate AutoMigrates the models before applying PostgreSQL-specific
// capabilities that GORM cannot express.
func Migrate(ctx context.Context, dbManager datastore.Manager, migrationsDirPath string) error {
	dbPool := dbManager.GetPool(ctx, datastore.DefaultPoolName)
	return migratePool(ctx, dbPool, migrationsDirPath)
}

func migratePool(ctx context.Context, dbPool pool.Pool, migrationsDirPath string) error {
	log := util.Log(ctx)
	log.WithField("path", migrationsDirPath).Info("running database migrations")

	models := []any{
		&domain.Source{},
		&domain.CrawlJob{},
		&SourceRecipe{},
		&domain.Company{},
		&domain.CrawlRun{},
		&jobqueue.QueueRecord{},
		&jobqueue.OpportunityRecord{},
		&jobqueue.OpportunityIdentityRecord{},
		&jobqueue.OpportunitySourceRecord{},
		&jobqueue.IngestEventRecord{},
	}
	models = append(models, frontier.Schema()...)
	if err := dbPool.Migrate(ctx, migrationsDirPath, models...); err != nil {
		return fmt.Errorf("pool migrate: %w", err)
	}

	return nil
}
