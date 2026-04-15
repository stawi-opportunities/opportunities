package app

import (
	"context"
	"log/slog"
	"time"

	"stawi.jobs/internal/domain"
)

type DedupeRunner struct {
	store domain.JobStore
	log   *slog.Logger
}

func NewDedupeRunner(store domain.JobStore, log *slog.Logger) *DedupeRunner {
	return &DedupeRunner{store: store, log: log}
}

func (d *DedupeRunner) Run(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	d.log.Info("dedupe runner started")
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			d.log.Info("dedupe sweep completed")
		}
	}
}
