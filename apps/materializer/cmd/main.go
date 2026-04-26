// apps/materializer/cmd — entrypoint for the Manticore materializer.
//
// The materializer is now a pure Frame subscriber: it registers one handler
// per topic (canonicals, canonicals_expired, translations, embeddings) and
// upserts documents into Manticore's idx_opportunities_rt RT index as events arrive.
//
// There is no longer an Iceberg catalog scan, no watermark polling, and no
// leader-election lease — Frame's NATS JetStream consumer group handles all
// of that natively. Consumer lag is observable via the NATS Prometheus
// exporter's `nats_jetstream_consumer_num_pending` metric.
package main

import (
	"context"
	"log"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/searchindex"
	"github.com/stawi-opportunities/opportunities/pkg/telemetry"

	matcfg "github.com/stawi-opportunities/opportunities/apps/materializer/config"
	matsvc "github.com/stawi-opportunities/opportunities/apps/materializer/service"
)

func main() {
	ctx := context.Background()

	cfg, err := matcfg.Load()
	if err != nil {
		log.Fatalf("materializer: load config: %v", err)
	}

	// Frame service — NATS-backed pub/sub + OTEL.
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithConfig(&cfg),
	)
	defer svc.Stop(ctx)

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

	// Register Iceberg-table-level observables (compaction lag, snapshot age).
	// The materializer no longer holds watermarks, so the Valkey arg is nil.
	// Consumer lag is observable via NATS JetStream native metrics.
	telemetry.RegisterIcebergObservables(telemetry.IcebergObservablesConfig{
		TableIdents: nil, // materializer no longer scans Iceberg tables
	})

	// Wire Frame topic subscribers.
	service := matsvc.NewService(svc, mc, cfg.ManticoreBulkBatchSize)
	if err := service.RegisterSubscriptions(); err != nil {
		util.Log(ctx).WithError(err).Fatal("materializer: register subscriptions failed")
	}

	if err := svc.Run(ctx, ""); err != nil {
		util.Log(ctx).WithError(err).Fatal("materializer: frame.Run failed")
	}
}
