package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"stawi.jobs/internal/app"
	"stawi.jobs/internal/config"
	"stawi.jobs/internal/storage"
	"stawi.jobs/internal/telemetry"
)

func main() {
	cfg, err := config.Load("ops-control-plane")
	if err != nil {
		panic(err)
	}
	log := telemetry.NewLogger(cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	h := storage.NewStore(ctx, cfg, log)
	defer h.Close()

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: app.NewOpsRouter(h.Store), ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()
	log.Info("ops api listening", "addr", cfg.HTTPAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("ops api stopped with error", "error", err.Error())
		os.Exit(1)
	}
}
