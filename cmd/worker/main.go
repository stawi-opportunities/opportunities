package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"stawi.jobs/internal/app"
	"stawi.jobs/internal/config"
	"stawi.jobs/internal/indexer"
	"stawi.jobs/internal/queue"
	"stawi.jobs/internal/storage"
	"stawi.jobs/internal/telemetry"
)

func main() {
	cfg, err := config.Load("worker")
	if err != nil {
		panic(err)
	}
	log := telemetry.NewLogger(cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	h := storage.NewStore(ctx, cfg, log)
	defer h.Close()

	registry := app.NewConnectorRegistry(cfg)
	q := queue.New(cfg, log)
	idx := indexer.New(cfg, log)
	worker := app.NewWorker(cfg, h.Store, registry, q, idx, log)
	if err := worker.Run(ctx); err != nil && ctx.Err() == nil {
		log.Error("worker stopped with error", "error", err.Error())
		os.Exit(1)
	}
}
