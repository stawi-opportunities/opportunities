// Package counters wraps a Valkey/Redis client with the per-slug
// view + apply counters used by the api. Both totals (no TTL) and
// 24h-windowed counts (TTL 25h so a fresh INCR on the keyset slides
// the window) are kept under a flat `view:{slug}` / `apply:{slug}` /
// `view:{slug}:24h` / `apply:{slug}:24h` namespace.
//
// Atomicity comes for free from Valkey's INCR — a single user
// spam-clicking still bumps the counter exactly once per beacon. The
// OpenObserve event log records each individual hit with profile_id,
// so dedup-by-profile happens analytics-side rather than gating the
// counter at write-time.
//
// A nil *Client is safe: every method becomes a no-op so the api can
// run without Valkey configured (local dev, smoke tests).
package counters

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Counters is the package-level handle. Construct with NewClient (or
// pass a nil *Counters to disable the surface entirely).
type Counters struct {
	rdb *redis.Client
}

// Window24hTTL is the lifetime of the `:24h` keys. Picked one hour
// past 24h so a fresh INCR after the window rolls still has a value
// to expire — Valkey's INCR-then-EXPIRE is a single round-trip when
// pipelined, but EXPIRE on a fresh key needs a non-zero base.
const Window24hTTL = 25 * time.Hour

// NewClient parses a redis:// URL and returns a *Counters. Empty URL
// returns (nil, nil) so the caller can treat the result the same as
// "feature disabled" without branching.
func NewClient(url string) (*Counters, error) {
	if url == "" {
		return nil, nil
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	return &Counters{rdb: redis.NewClient(opts)}, nil
}

// Close releases the underlying TCP connections. Safe on nil.
func (c *Counters) Close() error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.Close()
}

// IncrView atomically increments the view counters for slug. Both the
// total `view:{slug}` and the 24h-windowed `view:{slug}:24h` are
// bumped in a single pipeline. Errors are returned for observability;
// callers may log-and-ignore — the analytics log captures the same
// event with much wider downstream tooling.
func (c *Counters) IncrView(ctx context.Context, slug string) error {
	return c.incrPair(ctx, "view:"+slug)
}

// IncrApply mirrors IncrView for the apply beacon.
func (c *Counters) IncrApply(ctx context.Context, slug string) error {
	return c.incrPair(ctx, "apply:"+slug)
}

// incrPair atomically increments key + key:24h, setting EXPIRE on
// the windowed key. The pipeline is fire-and-forget at the caller's
// layer (no logical retry), but the client driver retries idle
// connection errors transparently.
func (c *Counters) incrPair(ctx context.Context, base string) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	pipe := c.rdb.Pipeline()
	pipe.Incr(ctx, base)
	pipe.Incr(ctx, base+":24h")
	pipe.Expire(ctx, base+":24h", Window24hTTL)
	_, err := pipe.Exec(ctx)
	if err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	return nil
}

// Stats is the read-side aggregate returned by the public stats
// endpoint and embedded into search results.
type Stats struct {
	ViewsTotal   int64 `json:"views_total"`
	Views24h     int64 `json:"views_24h"`
	AppliesTotal int64 `json:"applies_total"`
	Applies24h   int64 `json:"applies_24h"`
}

// GetStats returns counts for one slug. Missing keys count as zero so
// the response is always well-formed; the only failure mode is a
// transport error.
func (c *Counters) GetStats(ctx context.Context, slug string) (Stats, error) {
	if c == nil || c.rdb == nil {
		return Stats{}, nil
	}
	keys := []string{
		"view:" + slug,
		"view:" + slug + ":24h",
		"apply:" + slug,
		"apply:" + slug + ":24h",
	}
	vals, err := c.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return Stats{}, err
	}
	out := Stats{}
	out.ViewsTotal = parseInt(vals[0])
	out.Views24h = parseInt(vals[1])
	out.AppliesTotal = parseInt(vals[2])
	out.Applies24h = parseInt(vals[3])
	return out, nil
}

// GetStatsBatch is the bulk variant used by the search response
// embedder. One MGET handles N slugs × 4 keys; missing keys → zero.
// Returns a map keyed by slug so callers can attach the stats inline
// to their result rows without an N+1 round-trip.
func (c *Counters) GetStatsBatch(ctx context.Context, slugs []string) (map[string]Stats, error) {
	out := map[string]Stats{}
	if c == nil || c.rdb == nil || len(slugs) == 0 {
		return out, nil
	}
	keys := make([]string, 0, len(slugs)*4)
	for _, s := range slugs {
		keys = append(keys,
			"view:"+s,
			"view:"+s+":24h",
			"apply:"+s,
			"apply:"+s+":24h",
		)
	}
	vals, err := c.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return out, err
	}
	for i, slug := range slugs {
		base := i * 4
		out[slug] = Stats{
			ViewsTotal:   parseInt(vals[base]),
			Views24h:     parseInt(vals[base+1]),
			AppliesTotal: parseInt(vals[base+2]),
			Applies24h:   parseInt(vals[base+3]),
		}
	}
	return out, nil
}

// parseInt converts an MGet-style any value (string or nil) to an
// int64. Anything that doesn't parse cleanly returns 0.
func parseInt(v any) int64 {
	s, ok := v.(string)
	if !ok || s == "" {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
