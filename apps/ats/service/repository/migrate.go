package repository

import (
	"context"
	"errors"

	"github.com/pitabwire/frame/v2/datastore"

	"github.com/stawi-opportunities/opportunities/apps/ats/service/models"
)

// Migrate applies ATS schema via Frame datastore manager (setup Job only).
func Migrate(ctx context.Context, dbManager datastore.Manager, migrationPath string) error {
	pool := dbManager.GetPool(ctx, datastore.DefaultMigrationPoolName)
	if pool == nil {
		// Fall back to default pool when migration pool is not configured (tests).
		pool = dbManager.GetPool(ctx, datastore.DefaultPoolName)
	}
	if pool == nil {
		return errors.New("ats: datastore pool is not initialized")
	}
	return dbManager.Migrate(ctx, pool, migrationPath, models.Schema()...)
}
