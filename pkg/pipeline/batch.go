// Package pipeline provides the worker and batch buffer for processing crawl
// requests through the full normalization and storage pipeline.
package pipeline

import (
	"context"
	"sync"
	"time"

	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/repository"
)

// BatchBuffer accumulates JobVariants and flushes them in batches to the
// JobRepository. Flushes are triggered either when the buffer reaches maxSize
// or when the background ticker fires.
type BatchBuffer struct {
	mu        sync.Mutex
	variants  []*domain.JobVariant
	repo      *repository.JobRepository
	maxSize   int
	flushTick *time.Ticker
	done      chan struct{}
}

// NewBatchBuffer creates a BatchBuffer and starts a background goroutine that
// flushes accumulated variants on every flushInterval tick.
func NewBatchBuffer(repo *repository.JobRepository, maxSize int, flushInterval time.Duration) *BatchBuffer {
	b := &BatchBuffer{
		variants:  make([]*domain.JobVariant, 0, maxSize),
		repo:      repo,
		maxSize:   maxSize,
		flushTick: time.NewTicker(flushInterval),
		done:      make(chan struct{}),
	}
	go b.backgroundFlush()
	return b
}

// backgroundFlush runs in a goroutine and flushes on each ticker tick until
// the done channel is closed.
func (b *BatchBuffer) backgroundFlush() {
	for {
		select {
		case <-b.flushTick.C:
			_ = b.Flush(context.Background())
		case <-b.done:
			return
		}
	}
}

// Add appends a variant to the buffer. If the buffer size reaches maxSize the
// buffer is flushed immediately before returning.
func (b *BatchBuffer) Add(ctx context.Context, variant domain.JobVariant) error {
	b.mu.Lock()
	b.variants = append(b.variants, &variant)
	shouldFlush := len(b.variants) >= b.maxSize
	b.mu.Unlock()

	if shouldFlush {
		return b.Flush(ctx)
	}
	return nil
}

// Flush swaps the current buffer with a fresh one and writes all accumulated
// variants to the repository. It is safe to call concurrently.
func (b *BatchBuffer) Flush(ctx context.Context) error {
	b.mu.Lock()
	if len(b.variants) == 0 {
		b.mu.Unlock()
		return nil
	}
	batch := b.variants
	b.variants = make([]*domain.JobVariant, 0, b.maxSize)
	b.mu.Unlock()

	return b.repo.UpsertVariants(ctx, batch)
}

// Close stops the background ticker, closes the done channel, and performs a
// final flush to drain any remaining variants.
func (b *BatchBuffer) Close(ctx context.Context) error {
	b.flushTick.Stop()
	close(b.done)
	return b.Flush(ctx)
}
