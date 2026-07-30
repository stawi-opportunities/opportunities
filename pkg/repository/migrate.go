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

// Migrate AutoMigrates crawl-plane table shape, then applies capability SQL.
//
// Crawl Neon owns sources, frontier, ingest queue/events, and crawl_jobs.
// Product catalog tables (opportunities, identities, lineage) are migrated by
// apps/matching against product Neon — not here.
//
// pool.Migrate AutoMigrates models before applying PostgreSQL-specific SQL.
func Migrate(ctx context.Context, dbManager datastore.Manager, migrationsDirPath string) error {
	dbPool := dbManager.GetPool(ctx, datastore.DefaultPoolName)
	return migratePool(ctx, dbPool, migrationsDirPath)
}

func migratePool(ctx context.Context, dbPool pool.Pool, migrationsDirPath string) error {
	log := util.Log(ctx)
	log.WithField("path", migrationsDirPath).Info("running crawl database migrations")

	// Crawl control + durable ingest only. No product catalog models.
	models := []any{
		&domain.Source{},
		&domain.CrawlJob{},
		&SourceRecipe{},
		&domain.CrawlRun{},
		&jobqueue.QueueRecord{},
		&jobqueue.IngestEventRecord{},
	}
	models = append(models, frontier.Schema()...)
	if err := dbPool.Migrate(ctx, migrationsDirPath, models...); err != nil {
		return fmt.Errorf("pool migrate: %w", err)
	}

	return nil
}
