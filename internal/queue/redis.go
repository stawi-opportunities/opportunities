package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"stawi.jobs/internal/domain"
)

type RedisQueue struct {
	client *redis.Client
	name   string
}

func NewRedis(addr, name string) *RedisQueue {
	if name == "" {
		name = "crawl_requests"
	}
	return &RedisQueue{
		client: redis.NewClient(&redis.Options{Addr: addr}),
		name:   name,
	}
}

func (q *RedisQueue) Publish(ctx context.Context, req domain.CrawlRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal crawl request: %w", err)
	}
	if err := q.client.LPush(ctx, q.name, data).Err(); err != nil {
		return fmt.Errorf("publish crawl request: %w", err)
	}
	return nil
}

func (q *RedisQueue) Consume(ctx context.Context) (<-chan domain.CrawlRequest, error) {
	out := make(chan domain.CrawlRequest)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			res, err := q.client.BRPop(ctx, 5*time.Second, q.name).Result()
			if err != nil {
				if err == redis.Nil {
					continue
				}
				if ctx.Err() != nil {
					return
				}
				continue
			}
			if len(res) != 2 {
				continue
			}
			var req domain.CrawlRequest
			if err := json.Unmarshal([]byte(res[1]), &req); err != nil {
				continue
			}
			select {
			case <-ctx.Done():
				return
			case out <- req:
			}
		}
	}()
	return out, nil
}

func (q *RedisQueue) Close() error { return q.client.Close() }
