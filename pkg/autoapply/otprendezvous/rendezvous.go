// Package otprendezvous is the short-lived hand-off point between the
// inbound OTP-email pipeline and an in-flight browser submission that is
// holding a Greenhouse application open, waiting for the emailed security
// code.
//
// The submitter blocks in Poll; the email webhook (or the Phase-2 manual
// injector) calls Put with the same key. Keys are derived from the
// candidate email + the company the application targets (see key.go) so a
// candidate may have several applications in flight without their codes
// crossing wires.
//
// Backed by Frame's cache.RawCache so the identical code path runs on an
// in-memory cache (single-instance / tests) and Valkey (multi-instance
// prod) with no branching — per the repo rule against a direct go-redis
// client (see pkg/counters).
package otprendezvous

import (
	"context"
	"errors"
	"time"

	"github.com/pitabwire/frame/cache"
)

// ErrTimeout is returned by Poll when no code arrives before the deadline.
var ErrTimeout = errors.New("otprendezvous: timed out waiting for code")

// keyPrefix namespaces every rendezvous entry in the shared cache.
const keyPrefix = "otp:gh:"

// defaultTTL bounds how long a Put-ed code lives if never consumed.
// Greenhouse codes expire on their side within minutes; we keep ours a
// touch longer so a slightly-late Poll still finds a valid code.
const defaultTTL = 5 * time.Minute

// pollInterval is how often Poll re-checks the cache while waiting.
const pollInterval = 2 * time.Second

// Rendezvous is the narrow seam shared by the Greenhouse submitter and
// the OTP ingress. Implementations must be safe for concurrent use.
type Rendezvous interface {
	// Clear drops any code currently stored under key. The submitter
	// calls this immediately before submitting so a stale code from a
	// prior attempt to the same (candidate, company) cannot be consumed
	// in place of the freshly-emailed one.
	Clear(ctx context.Context, key string) error
	// Poll blocks until a code is available for key or timeout elapses.
	// On success it consumes (deletes) the entry so a redelivered email
	// cannot replay it. Returns ErrTimeout when nothing arrives in time.
	Poll(ctx context.Context, key string, timeout time.Duration) (string, error)
	// Put stores code under key for a waiting Poll to pick up.
	Put(ctx context.Context, key, code string) error
}

// CacheRendezvous is the cache.RawCache-backed Rendezvous.
type CacheRendezvous struct {
	raw cache.RawCache
	ttl time.Duration
}

// New returns a CacheRendezvous over the supplied raw cache.
func New(raw cache.RawCache) *CacheRendezvous {
	return &CacheRendezvous{raw: raw, ttl: defaultTTL}
}

// NewInMemory returns a CacheRendezvous backed by an in-process cache.
// Suitable for single-instance deployments and tests. Multi-instance
// deployments must use New with a Valkey-backed raw cache so the webhook
// instance and the browser-holding instance share state.
func NewInMemory() *CacheRendezvous {
	return New(cache.NewInMemoryCache())
}

// Clear implements Rendezvous.
func (r *CacheRendezvous) Clear(ctx context.Context, key string) error {
	return r.raw.Delete(ctx, keyPrefix+key)
}

// Put implements Rendezvous.
func (r *CacheRendezvous) Put(ctx context.Context, key, code string) error {
	return r.raw.Set(ctx, keyPrefix+key, []byte(code), r.ttl)
}

// MarkSeen atomically records messageID as processed, returning true the
// first time and false on any later call within the TTL. The OTP-email
// webhook uses it to drop provider redeliveries. Atomicity comes from the
// cache's INCR, so concurrent duplicate deliveries can't both win.
func (r *CacheRendezvous) MarkSeen(ctx context.Context, messageID string) (bool, error) {
	key := "otpseen:" + messageID
	n, err := r.raw.Increment(ctx, key, 1)
	if err != nil {
		return false, err
	}
	if n == 1 {
		_ = r.raw.Expire(ctx, key, r.ttl)
	}
	return n == 1, nil
}

// Poll implements Rendezvous. It checks once immediately, then on each
// pollInterval tick, until the code arrives, the deadline fires, or ctx
// is cancelled.
func (r *CacheRendezvous) Poll(ctx context.Context, key string, timeout time.Duration) (string, error) {
	full := keyPrefix + key

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		if b, found, err := r.raw.Get(ctx, full); err == nil && found && len(b) > 0 {
			_ = r.raw.Delete(ctx, full) // consume — single-use
			return string(b), nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timer.C:
			return "", ErrTimeout
		case <-ticker.C:
		}
	}
}
