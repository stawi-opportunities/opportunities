package storage

import (
	"context"
	"log/slog"

	"stawi.jobs/internal/config"
	"stawi.jobs/internal/domain"
)

type StoreHandle struct {
	Store domain.UnitOfWork
	Close func()
}

func NewStore(ctx context.Context, cfg config.Config, log *slog.Logger) StoreHandle {
	if cfg.PostgresDSN == "" {
		log.Warn("POSTGRES_DSN empty, using in-memory store")
		return StoreHandle{Store: NewMemoryStore(), Close: func() {}}
	}
	pg, err := NewPostgresStore(ctx, cfg.PostgresDSN)
	if err != nil {
		log.Warn("postgres unavailable, fallback to in-memory store", "error", err.Error())
		return StoreHandle{Store: NewMemoryStore(), Close: func() {}}
	}
	return StoreHandle{Store: pg, Close: pg.Close}
}
