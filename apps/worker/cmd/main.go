// Command worker drains PostgreSQL job ingestion work into the canonical
// PostgreSQL serving tables.
package main

import (
	"context"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/datastore"
	"github.com/pitabwire/util"

	workercfg "github.com/stawi-opportunities/opportunities/apps/worker/config"
	workersvc "github.com/stawi-opportunities/opportunities/apps/worker/service"
	"github.com/stawi-opportunities/opportunities/pkg/jobqueue"
)

func main() {
	ctx := context.Background()
	cfg, err := workercfg.Load()
	if err != nil {
		util.Log(ctx).WithError(err).Fatal("worker: load config")
	}

	ctx, svc := frame.NewServiceWithContext(ctx, frame.WithConfig(&cfg), frame.WithDatastore())
	defer svc.Stop(ctx)
	pool := svc.DatastoreManager().GetPool(ctx, datastore.DefaultPoolName)
	if pool == nil {
		util.Log(ctx).Fatal("worker: DATABASE_URL is required")
	}

	processor := workersvc.NewPostgresProcessor(jobqueue.New(pool.DB), "",
		cfg.PostgresBatchSize, cfg.PostgresConcurrency, cfg.PostgresPollInterval,
		cfg.PostgresLease, cfg.PostgresMaxAttempts)
	go processor.Run(ctx)
	util.Log(ctx).Info("worker: PostgreSQL ingestion processor started")

	svc.Init(ctx)
	if err := svc.Run(ctx, ""); err != nil {
		util.Log(ctx).WithError(err).Fatal("worker: frame.Run failed")
	}
}
