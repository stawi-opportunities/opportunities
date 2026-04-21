package kv

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// DedupStore maps hard_key → cluster_id. No TTL — a hard_key's
// cluster assignment is permanent until explicit reset during
// operational cluster-rebuild.
type DedupStore struct {
	c *Client
}

// NewDedupStore wraps a Client.
func NewDedupStore(c *Client) *DedupStore { return &DedupStore{c: c} }

const dedupPrefix = "dedup:"

// Get returns (cluster_id, true, nil) on hit, ("", false, nil) on miss.
// Errors are wrapped with a kv: prefix.
func (s *DedupStore) Get(ctx context.Context, hardKey string) (string, bool, error) {
	val, err := s.c.rdb.Get(ctx, dedupPrefix+hardKey).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("kv: dedup get: %w", err)
	}
	return val, true, nil
}

// Set records an assignment. Idempotent — same key, same value
// is a no-op; same key, different value overwrites (operational
// re-cluster path).
func (s *DedupStore) Set(ctx context.Context, hardKey, clusterID string) error {
	if err := s.c.rdb.Set(ctx, dedupPrefix+hardKey, clusterID, 0).Err(); err != nil {
		return fmt.Errorf("kv: dedup set: %w", err)
	}
	return nil
}
