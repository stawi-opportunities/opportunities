package repository

import (
	"context"
	"errors"

	"github.com/pitabwire/frame/v2/datastore"

	"github.com/stawi-opportunities/opportunities/apps/calendar/service/models"
)

// Migrate applies calendar schema (setup Job only).
func Migrate(ctx context.Context, dbManager datastore.Manager, migrationPath string) error {
	pool := dbManager.GetPool(ctx, datastore.DefaultMigrationPoolName)
	if pool == nil {
		pool = dbManager.GetPool(ctx, datastore.DefaultPoolName)
	}
	if pool == nil {
		return errors.New("calendar: datastore pool is not initialized")
	}
	return dbManager.Migrate(ctx, pool, migrationPath, models.Schema()...)
}
