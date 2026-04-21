// apps/writer/cmd — entrypoint for the event-log writer service.
//
// The writer is a disposable pod that subscribes to every job pipeline
// topic, buffers incoming events per (partition_dt, partition_secondary),
// and flushes Parquet files to R2 on size/count/time triggers.
package main

import (
	"context"
	"net/http"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/eventlog"

	writercfg "stawi.jobs/apps/writer/config"
	writersvc "stawi.jobs/apps/writer/service"
)

func main() {
	ctx := context.Background()

	cfg, err := writercfg.Load()
	if err != nil {
		util.Log(ctx).WithError(err).Fatal("writer: load config: ")
	}

	opts := []frame.Option{
		frame.WithConfig(&cfg),
	}

	ctx, svc := frame.NewServiceWithContext(ctx, opts...)
	defer svc.Stop(ctx)

	client := eventlog.NewClient(eventlog.R2Config{
		AccountID:       cfg.R2AccountID,
		AccessKeyID:     cfg.R2AccessKeyID,
		SecretAccessKey: cfg.R2SecretAccessKey,
		Bucket:          cfg.R2Bucket,
		Endpoint:        cfg.R2Endpoint,
		UsePathStyle:    cfg.R2UsePathStyle,
	})
	uploader := eventlog.NewUploader(client, cfg.R2Bucket)

	buffer := writersvc.NewBuffer(writersvc.Thresholds{
		MaxEvents:   cfg.FlushMaxEvents,
		MaxBytes:    cfg.FlushMaxBytes,
		MaxInterval: cfg.FlushMaxInterval,
	})

	service := writersvc.NewService(svc, buffer, uploader, cfg.FlushMaxInterval)
	if err := service.RegisterSubscriptions(eventsv1.AllTopics()); err != nil {
		util.Log(ctx).WithError(err).Fatal("writer: register subscriptions failed")
	}

	go func() {
		if err := service.RunFlusher(ctx); err != nil {
			util.Log(ctx).WithError(err).Error("writer: flusher exited")
		}
	}()

	// Admin HTTP endpoints for manual compaction triggers.
	reader := eventlog.NewReader(client, cfg.R2Bucket)
	compactor := writersvc.NewCompactor(client, reader, uploader, cfg.R2Bucket)

	adminMux := http.NewServeMux()
	adminMux.HandleFunc("POST /_admin/compact/hourly", writersvc.CompactHourlyHandler(compactor))
	adminMux.HandleFunc("POST /_admin/compact/daily", writersvc.CompactDailyHandler(compactor))

	svc.Init(ctx, frame.WithHTTPHandler(adminMux))

	if err := svc.Run(ctx, ""); err != nil {
		util.Log(ctx).WithError(err).Fatal("writer: frame.Run failed")
	}
}
