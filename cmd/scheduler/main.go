package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"stawi.jobs/internal/app"
	"stawi.jobs/internal/config"
	"stawi.jobs/internal/queue"
	"stawi.jobs/internal/storage"
	"stawi.jobs/internal/telemetry"
)

func main() {
	cfg, err := config.Load("scheduler")
	if err != nil {
		panic(err)
	}
	log := telemetry.NewLogger(cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	h := storage.NewStore(ctx, cfg, log)
	defer h.Close()

	q := queue.New(cfg, log)
	scheduler := app.NewScheduler(cfg, h.Store, q, log)
	if err := scheduler.Run(ctx); err != nil && ctx.Err() == nil {
		log.Error("scheduler stopped with error", "error", err.Error())
		os.Exit(1)
	}
}
