// apps/materializer/cmd — entrypoint for the Iceberg → Manticore
// materializer. The pod polls each Iceberg table (jobs.canonicals,
// jobs.embeddings, jobs.translations) every 15 s, decodes only the
// data files added since the last Valkey watermark, and bulk-upserts
// them into Manticore's idx_jobs_rt RT index.
package main

import (
	"context"
	"log"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"
	"github.com/redis/go-redis/v9"

	"stawi.jobs/pkg/eventlog"
	"stawi.jobs/pkg/icebergclient"
	"stawi.jobs/pkg/searchindex"
	"stawi.jobs/pkg/telemetry"

	matcfg "stawi.jobs/apps/materializer/config"
	matsvc "stawi.jobs/apps/materializer/service"
)

func main() {
	ctx := context.Background()

	cfg, err := matcfg.Load()
	if err != nil {
		log.Fatalf("materializer: load config: %v", err)
	}

	// Frame service — no WithDatastore() since the materializer no
	// longer uses Postgres for watermarks (moved to Valkey).
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithConfig(&cfg),
	)
	defer svc.Stop(ctx)

	// Iceberg SQL catalog backed by Postgres + R2.
	cat, err := icebergclient.LoadCatalog(ctx, icebergclient.CatalogConfig{
		Name:              "stawi",
		URI:               cfg.IcebergCatalogURI,
		Warehouse:         "s3://" + cfg.R2Bucket + "/iceberg",
		R2Endpoint:        cfg.R2Endpoint,
		R2AccessKeyID:     cfg.R2AccessKeyID,
		R2SecretAccessKey: cfg.R2SecretAccessKey,
		R2Region:          cfg.R2Region,
	})
	if err != nil {
		util.Log(ctx).WithError(err).Fatal("materializer: catalog load failed")
	}

	// R2 reader — fetches raw Parquet bytes by object key.
	r2Client := eventlog.NewClient(eventlog.R2Config{
		AccountID:       cfg.R2AccountID,
		AccessKeyID:     cfg.R2AccessKeyID,
		SecretAccessKey: cfg.R2SecretAccessKey,
		Bucket:          cfg.R2Bucket,
		Endpoint:        cfg.R2Endpoint,
		UsePathStyle:    cfg.R2UsePathStyle,
	})
	reader := eventlog.NewReader(r2Client, cfg.R2Bucket)

	// Manticore client.
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

	// Valkey watermark client.
	kvOpts, err := redis.ParseURL(cfg.ValkeyURL)
	if err != nil {
		util.Log(ctx).WithError(err).Fatal("materializer: parse valkey URL failed")
	}
	kvClient := redis.NewClient(kvOpts)
	wm := matsvc.NewWatermark(kvClient)

	// Register Iceberg observables for materializer-lag and table-state gauges.
	// The Valkey client enables the materializer-lag callback which reads
	// mat:snap:<table> watermarks and compares them against the current
	// Iceberg snapshot timestamp.
	telemetry.RegisterIcebergObservables(telemetry.IcebergObservablesConfig{
		Catalog:     cat,
		TableIdents: icebergclient.AppendOnlyTables,
		Valkey:      kvClient,
	})

	service := matsvc.NewService(cat, reader, cfg.R2Bucket, mc, wm, cfg.PollInterval, cfg.ManticoreBulkBatchSize)

	go func() {
		if err := service.Run(ctx); err != nil {
			util.Log(ctx).WithError(err).Error("materializer: run exited")
		}
	}()

	if err := svc.Run(ctx, ""); err != nil {
		util.Log(ctx).WithError(err).Fatal("materializer: frame.Run failed")
	}
}
