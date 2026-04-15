package app

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"stawi.jobs/internal/config"
	"stawi.jobs/internal/domain"
	"stawi.jobs/internal/queue"
	"stawi.jobs/internal/storage"
)

func TestScheduleOncePublishes(t *testing.T) {
	store := storage.NewMemoryStore()
	_, err := store.UpsertSource(context.Background(), domain.Source{
		Type:             domain.SourceGreenhouse,
		BaseURL:          "https://boards.greenhouse.io/example",
		Country:          "US",
		Status:           domain.SourceActive,
		CrawlIntervalSec: 60,
		NextCrawlAt:      time.Now().UTC().Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("seed source: %v", err)
	}
	q := queue.NewMemoryQueue(10)
	s := NewScheduler(config.Config{ScheduleBatchSize: 10}, store, q, slog.Default())
	if err := s.scheduleOnce(context.Background()); err != nil {
		t.Fatalf("schedule once: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ch, err := q.Consume(ctx)
	if err != nil {
		t.Fatalf("consume queue: %v", err)
	}
	select {
	case <-ctx.Done():
		t.Fatal("expected a queued message")
	case <-ch:
	}
}
