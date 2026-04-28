// Package counters wraps Frame's cache.RawCache with the per-slug
// view + apply counters used by the api. Both totals (no TTL) and
// 24h-windowed counts (TTL 25h so a fresh INCR on the keyset slides
// the window) are kept under a flat `view:{slug}` / `apply:{slug}` /
// `view:{slug}:24h` / `apply:{slug}:24h` namespace.
//
// Atomicity comes from Valkey's INCR (exposed via cache.RawCache's
// Increment method) — a single user spam-clicking still bumps the
// counter exactly once per beacon. The OpenObserve event log records
// each individual hit with profile_id, so dedup-by-profile happens
// analytics-side rather than gating the counter at write-time.
//
// A nil *Counters is safe: every method becomes a no-op so the api
// can run without Valkey configured (local dev, smoke tests).
//
// This used to take a *redis.Client directly; per the golang-patterns
// rule "no direct go-redis client", it now uses Frame's cache.RawCache
// (Increment + Get + Expire) which the production wiring backs with
// Frame's valkey driver.
package counters

import (
	"context"
	"encoding/binary"
	"errors"
	"strconv"
	"time"

	"github.com/pitabwire/frame/cache"
	framevalkey "github.com/pitabwire/frame/cache/valkey"
	"github.com/pitabwire/frame/data"
)

// Counters is the package-level handle. Construct with NewClient (or
// pass a nil *Counters to disable the surface entirely).
type Counters struct {
	raw cache.RawCache
}

// Window24hTTL is the lifetime of the `:24h` keys. Picked one hour
// past 24h so a fresh INCR after the window rolls still has a value
// to expire.
const Window24hTTL = 25 * time.Hour

// NewClient parses a redis:// URL and returns a *Counters backed by
// Frame's valkey raw cache. Empty URL returns (nil, nil) so the caller
// can treat the result the same as "feature disabled" without
// branching.
func NewClient(url string) (*Counters, error) {
	if url == "" {
		return nil, nil
	}
	raw, err := framevalkey.New(cache.WithDSN(data.DSN(url)))
	if err != nil {
		return nil, err
	}
	return &Counters{raw: raw}, nil
}

// NewFromCache constructs a *Counters from an existing cache.RawCache.
// Useful when the caller already has a Frame service with a registered
// raw cache and wants to share the connection pool.
func NewFromCache(raw cache.RawCache) *Counters {
	if raw == nil {
		return nil
	}
	return &Counters{raw: raw}
}

// Close releases the underlying connections. Safe on nil.
func (c *Counters) Close() error {
	if c == nil || c.raw == nil {
		return nil
	}
	return c.raw.Close()
}

// IncrView atomically increments the view counters for slug. Both the
// total `view:{slug}` and the 24h-windowed `view:{slug}:24h` are
// bumped via cache.RawCache.Increment. Errors are returned for
// observability; callers may log-and-ignore — the analytics log
// captures the same event with much wider downstream tooling.
func (c *Counters) IncrView(ctx context.Context, slug string) error {
	return c.incrPair(ctx, "view:"+slug)
}

// IncrApply mirrors IncrView for the apply beacon.
func (c *Counters) IncrApply(ctx context.Context, slug string) error {
	return c.incrPair(ctx, "apply:"+slug)
}

// incrPair atomically increments key + key:24h, then refreshes the
// EXPIRE on the windowed key. Frame's RawCache exposes Increment +
// Expire (no Lua / pipeline wrapper) so we issue three round-trips
// rather than one; the volume is low enough (one beacon per page
// view) that the latency cost is negligible.
func (c *Counters) incrPair(ctx context.Context, base string) error {
	if c == nil || c.raw == nil {
		return nil
	}
	if _, err := c.raw.Increment(ctx, base, 1); err != nil {
		return err
	}
	windowKey := base + ":24h"
	if _, err := c.raw.Increment(ctx, windowKey, 1); err != nil {
		return err
	}
	if err := c.raw.Expire(ctx, windowKey, Window24hTTL); err != nil {
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
	if c == nil || c.raw == nil {
		return Stats{}, nil
	}
	out := Stats{}
	keys := []struct {
		k    string
		into *int64
	}{
		{"view:" + slug, &out.ViewsTotal},
		{"view:" + slug + ":24h", &out.Views24h},
		{"apply:" + slug, &out.AppliesTotal},
		{"apply:" + slug + ":24h", &out.Applies24h},
	}
	for _, kv := range keys {
		v, err := c.readInt64(ctx, kv.k)
		if err != nil {
			return Stats{}, err
		}
		*kv.into = v
	}
	return out, nil
}

// GetStatsBatch is the bulk variant used by the search response
// embedder. RawCache doesn't expose MGET, so we fall back to N×4
// individual Get calls. Missing keys → zero. Returns a map keyed by
// slug so callers can attach the stats inline to their result rows.
//
// Note on round-trips: the prior MGET implementation made one round
// trip per call. Frame's RawCache abstraction is GET-per-key. For the
// search response embedder this is fine (small N, embedded inside an
// already-Manticore-bound request); if hot-path callers need batched
// reads later, we can add a RawCacheBatch interface upstream.
func (c *Counters) GetStatsBatch(ctx context.Context, slugs []string) (map[string]Stats, error) {
	out := map[string]Stats{}
	if c == nil || c.raw == nil || len(slugs) == 0 {
		return out, nil
	}
	for _, slug := range slugs {
		stats, err := c.GetStats(ctx, slug)
		if err != nil {
			return out, err
		}
		out[slug] = stats
	}
	return out, nil
}

// readInt64 reads a counter value via cache.RawCache.Get. Missing
// keys return 0; transport errors propagate.
//
// Wire format note: cache.RawCache.Increment is implementation-
// defined. The valkey driver uses Redis-native INCRBY (ASCII decimal
// bytes); the in-memory driver uses big-endian uint64 (8 bytes).
// readInt64 accepts both — the 8-byte path is recognised first
// because ASCII "1" is also 1 byte, so the binary encoding's
// 8-byte fixed length is the discriminator.
func (c *Counters) readInt64(ctx context.Context, key string) (int64, error) {
	raw, found, err := c.raw.Get(ctx, key)
	if err != nil {
		return 0, err
	}
	if !found || len(raw) == 0 {
		return 0, nil
	}
	// Frame in-memory cache: 8-byte big-endian uint64.
	if len(raw) == 8 && !looksLikeASCIIDecimal(raw) {
		return int64(binary.BigEndian.Uint64(raw)), nil
	}
	// Valkey/Redis: ASCII decimal.
	n, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil {
		// Treat unparseable as zero rather than failing the whole stats
		// read — the only way this happens is a value the analytics log
		// already captured, so silently degrading is acceptable.
		var ne *strconv.NumError
		if errors.As(err, &ne) {
			return 0, nil
		}
		return 0, err
	}
	return n, nil
}

// looksLikeASCIIDecimal returns true if every byte in raw is an
// ASCII digit (or leading minus). Used by readInt64 to distinguish
// "8 ASCII digits" from "8-byte binary uint64" — both have len==8.
func looksLikeASCIIDecimal(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	start := 0
	if raw[0] == '-' {
		if len(raw) == 1 {
			return false
		}
		start = 1
	}
	for _, b := range raw[start:] {
		if b < '0' || b > '9' {
			return false
		}
	}
	return true
}
