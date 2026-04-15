package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"stawi.jobs/internal/domain"
)

type Queue interface {
	Publish(ctx context.Context, req domain.CrawlRequest) error
	Consume(ctx context.Context) (<-chan domain.CrawlRequest, error)
}

type MemoryQueue struct {
	mu sync.Mutex
	ch chan domain.CrawlRequest
}

func NewMemoryQueue(buffer int) *MemoryQueue {
	if buffer <= 0 {
		buffer = 1024
	}
	return &MemoryQueue{ch: make(chan domain.CrawlRequest, buffer)}
}

func (q *MemoryQueue) Publish(ctx context.Context, req domain.CrawlRequest) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case q.ch <- req:
		return nil
	}
}

func (q *MemoryQueue) Consume(ctx context.Context) (<-chan domain.CrawlRequest, error) {
	out := make(chan domain.CrawlRequest)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-q.ch:
				select {
				case <-ctx.Done():
					return
				case out <- msg:
				}
			}
		}
	}()
	return out, nil
}

func Encode(req domain.CrawlRequest) ([]byte, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal crawl request: %w", err)
	}
	return data, nil
}
