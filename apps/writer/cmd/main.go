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

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/icebergclient"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/telemetry"

	writercfg "github.com/stawi-opportunities/opportunities/apps/writer/config"
	writersvc "github.com/stawi-opportunities/opportunities/apps/writer/service"
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

	// Load the opportunity-kinds registry at boot. Phase 1 only loads + logs;
	// later phases consult the registry on the publish/index paths.
	reg, err := opportunity.LoadFromDir(cfg.OpportunityKindsDir)
	if err != nil {
		util.Log(ctx).WithError(err).Fatal("opportunity registry: load failed")
	}
	util.Log(ctx).WithField("kinds", reg.Known()).Info("opportunity registry: loaded")

	// Open the shared cluster Iceberg REST catalog (Lakekeeper).
	cat, err := icebergclient.LoadCatalog(ctx, icebergclient.CatalogConfig{
		Name:       cfg.IcebergCatalogName,
		URI:        cfg.IcebergCatalogURI,
		Warehouse:  cfg.IcebergWarehouse,
		OAuthToken: cfg.IcebergCatalogToken,
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

	// Register OTel Iceberg observables (catalog-backed gauges).
	// Must be called after the catalog is loaded.
	telemetry.RegisterIcebergObservables(telemetry.IcebergObservablesConfig{
		Catalog:     cat,
		TableIdents: writersvc.AppendOnlyTables,
	})

	// Register writer buffer stats for the buffer gauges.
	telemetry.RegisterBufferStats(func(topic string) (int, int) {
		return buffer.StatsForTopic(topic)
	}, eventsv1.AllTopics())

	// Admin HTTP mux — lightweight, not exposed to the public internet.
	// Trustage fires POST /_admin/expire-snapshots nightly and
	// POST /_admin/compact every 2 h via the in-cluster service DNS
	// (opportunities-writer.opportunities.svc).
	expireCfg := writersvc.ExpireSnapshotsConfig{
		OlderThan:          time.Duration(cfg.SnapshotRetentionDays) * 24 * time.Hour,
		MinSnapshotsToKeep: cfg.MinSnapshotsToKeep,
		PerTableTimeout:    5 * time.Minute,
		Parallelism:        4,
	}
	compactCfg := writersvc.CompactConfig{
		TargetFileSize:    cfg.CompactTargetFileSize,
		MinFileSize:       cfg.CompactMinFileSize,
		MaxInputPerCommit: cfg.CompactMaxInputPerCommit,
		PerTableTimeout:   cfg.CompactPerTableTimeout,
		Parallelism:       cfg.CompactParallelism,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /_admin/expire-snapshots",
		writersvc.ExpireSnapshotsHandler(cat, expireCfg))
	mux.HandleFunc("POST /_admin/compact",
		writersvc.CompactHandler(cat, compactCfg))

	svc.Init(ctx, frame.WithHTTPHandler(mux))

	if err := svc.Run(ctx, ""); err != nil {
		util.Log(ctx).WithError(err).Fatal("writer: frame.Run failed")
	}
}
