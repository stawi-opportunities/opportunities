// apps/materializer/cmd — entrypoint for the event-log → Manticore
// materializer. The pod polls R2 every 15 s for new Parquet files
// under tracked prefixes and upserts their rows into Manticore's
// idx_jobs_rt RT index.
package main

import (
	"context"
	"log"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/datastore"
	"github.com/pitabwire/util"

	"stawi.jobs/pkg/eventlog"
	"stawi.jobs/pkg/repository"
	"stawi.jobs/pkg/searchindex"

	matcfg "stawi.jobs/apps/materializer/config"
	matsvc "stawi.jobs/apps/materializer/service"
)

func main() {
	ctx := context.Background()

	cfg, err := matcfg.Load()
	if err != nil {
		log.Fatalf("materializer: load config: %v", err)
	}

	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithConfig(&cfg),
		frame.WithDatastore(),
	)
	defer svc.Stop(ctx)

	// Reader side — R2 list + get.
	r2Client := eventlog.NewClient(eventlog.R2Config{
		AccountID:       cfg.R2AccountID,
		AccessKeyID:     cfg.R2AccessKeyID,
		SecretAccessKey: cfg.R2SecretAccessKey,
		Bucket:          cfg.R2Bucket,
		Endpoint:        cfg.R2Endpoint,
		UsePathStyle:    cfg.R2UsePathStyle,
	})
	reader := eventlog.NewReader(r2Client, cfg.R2Bucket)

	// Manticore client (HTTP JSON API).
	mc, err := searchindex.Open(searchindex.Config{
		URL:     cfg.ManticoreURL,
		Timeout: cfg.ManticoreTimeout,
	})
	if err != nil {
		util.Log(ctx).WithError(err).Fatal("materializer: open manticore failed")
	}
	defer func() { _ = mc.Close() }()

	if err := searchindex.Apply(ctx, mc); err != nil {
		util.Log(ctx).WithError(err).Fatal("materializer: apply schema failed")
	}

	// Watermarks from Postgres via the Frame service's DB handle.
	// Pattern mirrors apps/crawler/cmd/main.go: WithDatastore() wires
	// the pool; DatastoreManager().GetPool() exposes pool.DB as the
	// func(ctx, readOnly) *gorm.DB closure the repositories expect.
	pool := svc.DatastoreManager().GetPool(ctx, datastore.DefaultPoolName)
	dbFn := pool.DB

	watermarks := repository.NewWatermarkRepository(dbFn)

	indexer := matsvc.NewIndexer(mc)
	service := matsvc.NewService(reader, indexer, watermarks, cfg.Prefixes, cfg.PollInterval, cfg.ListBatchSize)

	go func() {
		if err := service.Run(ctx); err != nil {
			util.Log(ctx).WithError(err).Error("materializer: run exited")
		}
	}()

	if err := svc.Run(ctx, ""); err != nil {
		util.Log(ctx).WithError(err).Fatal("materializer: frame.Run failed")
	}
}
