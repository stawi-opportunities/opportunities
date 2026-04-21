// Package kv is a thin wrapper around go-redis for the worker
// pipeline's two stateful lookups: dedup (hard_key → cluster_id)
// and cluster snapshots (cluster_id → compact canonical view used
// by the canonical-merge stage).
package kv

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Config describes how to reach Redis/Valkey.
type Config struct {
	// URL is a go-redis connection URL, e.g. "redis://host:6379/0".
	URL string
}

// Client wraps a *redis.Client so downstream stores can share it.
type Client struct {
	rdb *redis.Client
}

// Open parses the URL and connects. The caller is responsible for Close.
func Open(cfg Config) (*Client, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("kv: empty URL")
	}
	opt, err := redis.ParseURL(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("kv: parse url: %w", err)
	}
	return &Client{rdb: redis.NewClient(opt)}, nil
}

// Ping checks connectivity.
func (c *Client) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

// Close releases the connection pool.
func (c *Client) Close() error { return c.rdb.Close() }

// Redis exposes the underlying client for stores that need
// pipelined / scripted operations beyond simple GET/SET.
func (c *Client) Redis() *redis.Client { return c.rdb }
