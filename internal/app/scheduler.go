package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"stawi.jobs/internal/config"
	"stawi.jobs/internal/domain"
	"stawi.jobs/internal/queue"
)

type Scheduler struct {
	cfg   config.Config
	store domain.UnitOfWork
	queue queue.Queue
	log   *slog.Logger
}

func NewScheduler(cfg config.Config, store domain.UnitOfWork, q queue.Queue, log *slog.Logger) *Scheduler {
	return &Scheduler{cfg: cfg, store: store, queue: q, log: log}
}

func (s *Scheduler) Run(ctx context.Context) error {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	if err := s.scheduleOnce(ctx); err != nil {
		s.log.Error("initial schedule failed", "error", err.Error())
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := s.scheduleOnce(ctx); err != nil {
				s.log.Error("scheduled run failed", "error", err.Error())
			}
		}
	}
}

func (s *Scheduler) scheduleOnce(ctx context.Context) error {
	now := time.Now().UTC()
	sources, err := s.store.ListDueSources(ctx, now, s.cfg.ScheduleBatchSize)
	if err != nil {
		return fmt.Errorf("list due sources: %w", err)
	}
	for _, src := range sources {
		window := now.Truncate(time.Minute).Format(time.RFC3339)
		idk := hash(fmt.Sprintf("%d|%s|%s", src.ID, src.Type, window))
		job, err := s.store.CreateCrawlJob(ctx, domain.CrawlJob{
			SourceID:       src.ID,
			ScheduledAt:    now,
			Status:         domain.CrawlScheduled,
			Attempt:        1,
			IdempotencyKey: idk,
		})
		if err != nil {
			s.log.Error("create crawl job failed", "source_id", src.ID, "error", err.Error())
			continue
		}
		req := domain.CrawlRequest{SourceID: src.ID, SourceType: src.Type, ScheduledFor: job.ScheduledAt, Attempt: job.Attempt}
		if err := s.queue.Publish(ctx, req); err != nil {
			s.log.Error("publish crawl request failed", "source_id", src.ID, "error", err.Error())
			continue
		}
		next := now.Add(time.Duration(src.CrawlIntervalSec) * time.Second)
		if src.CrawlIntervalSec <= 0 {
			next = now.Add(6 * time.Hour)
		}
		if err := s.store.TouchSource(ctx, src.ID, next, src.HealthScore); err != nil {
			s.log.Error("touch source failed", "source_id", src.ID, "error", err.Error())
		}
	}
	s.log.Info("schedule completed", "due_sources", len(sources))
	return nil
}

func hash(v string) string {
	h := sha256.Sum256([]byte(v))
	return hex.EncodeToString(h[:])
}
