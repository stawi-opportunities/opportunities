// Package frontier implements the per-URL priority queue + per-host
// politeness state that drives D2's URL-level crawl scheduling.
//
// Replaces the per-source iterator model with a shared queue:
// connectors enqueue discovered URLs with a priority; the
// frontier-worker dequeues in priority order under per-host
// politeness constraints and runs the fetch + extract pipeline.
//
// The Postgres-backed implementation is in postgres.go; this file
// only defines the contract so unit tests can swap a fake.
package frontier

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"
)

// State enumerates the lifecycle of a queued URL.
type State string

// Lifecycle state constants. The Dequeue hot path keys on
// StatePending; the other states exist for diagnostics and the
// admin requeue surface.
const (
	StatePending  State = "pending"
	StateInFlight State = "in_flight"
	StateDone     State = "done"
	StateFailed   State = "failed"
	StateParked   State = "parked"
)

// URL is one row's worth of frontier state — both the enqueue
// payload (URLID is assigned by Enqueue) and the dequeue claim
// payload returned to a worker.
type URL struct {
	URLID         string
	CanonicalURL  string
	Host          string
	SourceID      string
	Priority      float64
	State         State
	Attempts      int
	EnqueuedAt    time.Time
	ClaimedAt     *time.Time
	NextAttemptAt *time.Time
	Metadata      json.RawMessage
}

// EnqueueResult is the per-row outcome from Enqueue. Inserted=false
// means the canonical_url_hash already existed — the existing row's
// priority may have been bumped if the new value was higher, but the
// row keeps its current state so in-flight URLs aren't reset.
type EnqueueResult struct {
	URLID    string
	Inserted bool
}

// Frontier is the operator-facing contract — the smallest surface
// the frontier-worker + connectors need to talk to the queue.
//
// Implementations are concurrency-safe; the production Postgres
// implementation uses SELECT ... FOR UPDATE SKIP LOCKED to serve
// Dequeue from many worker pods without double-claims.
type Frontier interface {
	// Enqueue inserts the URLs and returns per-row results.
	// Duplicates by canonical_url_hash are deduped against the
	// existing row: the higher of old/new priority wins, and the
	// row's state is left untouched.
	Enqueue(ctx context.Context, urls []URL) ([]EnqueueResult, error)

	// Dequeue atomically claims up to n URLs ordered by priority
	// DESC, enqueued_at ASC. Skips rows whose host's
	// next_eligible_at is in the future or whose host_state.
	// concurrency_now is at concurrency_max. The claim transaction
	// stamps state='in_flight', claimed_at=now(), claimed_by=workerID,
	// bumps attempts++, advances host_state.next_eligible_at by the
	// host's window_minutes, and increments concurrency_now.
	Dequeue(ctx context.Context, n int, workerID string) ([]URL, error)

	// Complete marks the URL done, decrements host_state.concurrency_now,
	// and bumps host_state.ok_count_24h.
	Complete(ctx context.Context, urlID string) error

	// Fail records the failure. When attempts < maxAttempts the URL
	// returns to state='pending' with next_attempt_at set by
	// exponential backoff (capped at 1h); otherwise transitions to
	// state='failed'. Decrements concurrency_now and bumps
	// host_state.err_count_24h either way.
	Fail(ctx context.Context, urlID string, runErr error, maxAttempts int) error
}

// ErrInvalidURL is returned by Enqueue when a row carries a
// canonical_url that cannot be parsed / lacks a host. Per-row
// errors don't fail the whole batch — Enqueue returns the
// successful results plus this error if any row was rejected.
var ErrInvalidURL = errors.New("frontier: invalid canonical URL")

// HostOf returns the lower-cased host portion of a URL with
// the port stripped. Used both at enqueue time and by the
// host_state backfill to make sure the queue and politeness
// table key on the same string for the same target.
//
// Returns "" when the URL can't be parsed or has no host —
// callers treat empty as a validation failure.
func HostOf(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	h := u.Hostname() // strips port
	return strings.ToLower(h)
}

// trackingParams are query parameters that don't change which resource a
// URL points at — analytics/referral tags. Stripping them keeps the
// dedup key stable across the same listing shared via different campaigns.
var trackingParams = map[string]struct{}{
	"fbclid": {}, "gclid": {}, "ref": {}, "source": {},
}

// CanonicalizeURL normalizes a URL for stable dedup + storage:
//   - lower-cases scheme and host
//   - drops the fragment
//   - removes tracking query params (utm_*, fbclid, gclid, ref, source)
//   - sorts remaining query params
//   - strips a trailing slash on the path (but leaves root "/")
//
// On parse error it returns the raw string unchanged so a weird-but-valid
// URL is never dropped — the caller still enqueues it, just un-normalized.
func CanonicalizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}

	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Fragment = ""
	u.RawFragment = ""

	if q := u.Query(); len(q) > 0 {
		for k := range q {
			lk := strings.ToLower(k)
			if _, drop := trackingParams[lk]; drop || strings.HasPrefix(lk, "utm_") {
				q.Del(k)
			}
		}
		// url.Values.Encode sorts by key, giving a stable ordering.
		u.RawQuery = q.Encode()
	}

	if u.Path != "/" {
		u.Path = strings.TrimSuffix(u.Path, "/")
	}

	return u.String()
}
