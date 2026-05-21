// pkg/matching/debounce_valkey.go
package matching

import (
	"context"
	"fmt"
	"time"

	"github.com/pitabwire/frame/cache"
	framevalkey "github.com/pitabwire/frame/cache/valkey"
	"github.com/pitabwire/frame/data"
)

// ValkeyDebouncer is the production Debouncer backed by Valkey/Redis.
// It uses Set-if-absent with TTL to ensure exactly one rematch per
// candidate per window across all pods.
//
// Note: Frame's cache.RawCache does not expose SETNX directly. This
// implementation uses Exists + Set which has a TOCTOU race window of
// one round-trip. In practice the race manifests as a duplicate
// rematch trigger during a thundering-herd burst across pods — an
// acceptable one-off event for the matching use-case. A true SETNX
// can be added if Frame exposes it upstream.
type ValkeyDebouncer struct {
	raw cache.RawCache
}

// NewValkeyDebouncer parses a redis:// URL and returns a Debouncer. An
// empty URL returns (nil, nil) so callers can fall back to the
// MemoryDebouncer when Valkey is unconfigured (local dev, tests).
func NewValkeyDebouncer(url string) (*ValkeyDebouncer, error) {
	if url == "" {
		return nil, nil
	}
	raw, err := framevalkey.New(cache.WithDSN(data.DSN(url)))
	if err != nil {
		return nil, fmt.Errorf("matching: valkey debouncer: %w", err)
	}
	return &ValkeyDebouncer{raw: raw}, nil
}

// Compile-time check that ValkeyDebouncer satisfies the Debouncer interface.
var _ Debouncer = (*ValkeyDebouncer)(nil)

// Acquire returns true on the first call for candidateID within ttl,
// false on subsequent calls. It writes a sentinel key with the given
// TTL on first acquire so the entry expires automatically.
func (d *ValkeyDebouncer) Acquire(ctx context.Context, candidateID string, ttl time.Duration) (bool, error) {
	key := "matching:debounce:" + candidateID
	exists, err := d.raw.Exists(ctx, key)
	if err != nil {
		return false, fmt.Errorf("matching: valkey exists: %w", err)
	}
	if exists {
		// Already debounced within the TTL window.
		return false, nil
	}
	if err := d.raw.Set(ctx, key, []byte("1"), ttl); err != nil {
		return false, fmt.Errorf("matching: valkey set: %w", err)
	}
	return true, nil
}
