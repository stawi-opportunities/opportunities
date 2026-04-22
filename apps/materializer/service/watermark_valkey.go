package service

// watermark_valkey.go — Valkey-backed snapshot watermark.
//
// Keys:  mat:snap:<namespace>.<table>  →  "<int64 snapshot ID>"
// Zero value (key absent or parse error) means cold start — scan from
// the first snapshot.

import (
	"context"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"
)

// Watermark reads and advances the last-processed Iceberg snapshot ID
// for a given table identifier (e.g. "jobs.canonicals").
type Watermark struct {
	kv *redis.Client
}

// NewWatermark wraps a redis.Client for watermark operations.
func NewWatermark(kv *redis.Client) *Watermark {
	return &Watermark{kv: kv}
}

// key returns the Valkey key for the given table identifier.
func (w *Watermark) key(ident string) string {
	return "mat:snap:" + ident
}

// Get returns the last-applied snapshot ID for ident. Returns 0 if the
// key is absent (cold start) or if the stored value is unparseable.
func (w *Watermark) Get(ctx context.Context, ident string) (int64, error) {
	v, err := w.kv.Get(ctx, w.key(ident)).Result()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("watermark get %q: %w", ident, err)
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		// Corrupt value — treat as cold start rather than hard failing.
		return 0, nil
	}
	return id, nil
}

// Set records snapID as the last-applied snapshot for ident. No TTL —
// watermarks are permanent until explicitly reset.
func (w *Watermark) Set(ctx context.Context, ident string, snapID int64) error {
	if err := w.kv.Set(ctx, w.key(ident), strconv.FormatInt(snapID, 10), 0).Err(); err != nil {
		return fmt.Errorf("watermark set %q: %w", ident, err)
	}
	return nil
}
