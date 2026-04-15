package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"stawi.jobs/internal/app"
	"stawi.jobs/internal/config"
	"stawi.jobs/internal/storage"
	"stawi.jobs/internal/telemetry"
)

func main() {
	cfg, err := config.Load("source-discovery")
	if err != nil {
		panic(err)
	}
	log := telemetry.NewLogger(cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	h := storage.NewStore(ctx, cfg, log)
	defer h.Close()

	svc := app.NewDiscovery(cfg, h.Store, log)
	if err := svc.Run(ctx); err != nil && ctx.Err() == nil {
		log.Error("source discovery stopped with error", "error", err.Error())
		os.Exit(1)
	}
}
