// apps/writer/cmd — entrypoint for the event-log writer service.
//
// The writer subscribes to every job-pipeline topic, buffers incoming
// events per (partition_dt, partition_secondary), and flushes them to
// Iceberg via Transaction.Append on size/count/time triggers.
package main

import (
	"context"
	"net/http"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/icebergclient"

	writercfg "stawi.jobs/apps/writer/config"
	writersvc "stawi.jobs/apps/writer/service"
)

func main() {
	ctx := context.Background()

	cfg, err := writercfg.Load()
	if err != nil {
		util.Log(ctx).WithError(err).Fatal("writer: load config")
	}

	opts := []frame.Option{
		frame.WithConfig(&cfg),
	}

	ctx, svc := frame.NewServiceWithContext(ctx, opts...)
	defer svc.Stop(ctx)

	// Open the Iceberg SQL catalog backed by Postgres + R2.
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
		util.Log(ctx).WithError(err).Fatal("writer: catalog load failed")
	}

	buffer := writersvc.NewBuffer(writersvc.Thresholds{
		MaxEvents:   cfg.FlushMaxEvents,
		MaxBytes:    cfg.FlushMaxBytes,
		MaxInterval: cfg.FlushMaxInterval,
	})

	wService := writersvc.NewService(svc, buffer, cat, cfg.FlushMaxInterval)
	if err := wService.RegisterSubscriptions(eventsv1.AllTopics()); err != nil {
		util.Log(ctx).WithError(err).Fatal("writer: register subscriptions failed")
	}

	go func() {
		if err := wService.RunFlusher(ctx); err != nil {
			util.Log(ctx).WithError(err).Error("writer: flusher exited")
		}
	}()

	// Admin HTTP mux — lightweight, not exposed to the public internet.
	// Trustage fires POST /_admin/expire-snapshots nightly via the
	// in-cluster service DNS (stawi-jobs-writer.stawi-jobs.svc).
	expireCfg := writersvc.ExpireSnapshotsConfig{
		OlderThan:          time.Duration(cfg.SnapshotRetentionDays) * 24 * time.Hour,
		MinSnapshotsToKeep: cfg.MinSnapshotsToKeep,
		PerTableTimeout:    5 * time.Minute,
		Parallelism:        4,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /_admin/expire-snapshots",
		writersvc.ExpireSnapshotsHandler(cat, expireCfg))

	svc.Init(ctx, frame.WithHTTPHandler(mux))

	if err := svc.Run(ctx, ""); err != nil {
		util.Log(ctx).WithError(err).Fatal("writer: frame.Run failed")
	}
}
