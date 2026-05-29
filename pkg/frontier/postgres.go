// pkg/frontier/postgres.go — Postgres implementation of Frontier.
//
// Pool factory follows the same shape as every repository in
// pkg/repository (func(ctx, readOnly bool) *gorm.DB). The hot
// path Dequeue runs inside db.Transaction so the SELECT ... FOR
// UPDATE SKIP LOCKED lock + the UPDATE land atomically; concurrent
// workers race on the row lock and never double-claim.
//
// PrepareStmt is disabled on the dequeue query: pgbouncer in
// transaction pooling mode collides on cached statement names
// across pooled connections (SQLSTATE 08P01). Mirrors the pattern
// at pkg/repository/source.go:441-454 (RefreshSignals/LoadSignals).
package frontier

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/rs/xid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PoolFn matches the GORM pool shape used by every other
// repository in this codebase. readOnly=true selects the
// read replica when one is configured.
type PoolFn func(ctx context.Context, readOnly bool) *gorm.DB

// PostgresFrontier is the production Frontier. Construct via
// NewPostgresFrontier(pool); the zero value is unusable.
type PostgresFrontier struct {
	db PoolFn

	// MaxBackoff caps the exponential schedule the Fail path uses
	// to compute next_attempt_at. Default 1h; smaller values are
	// useful in tests to keep retries observable.
	MaxBackoff time.Duration

	// BackoffBase is the first retry delay (2^0 * base). Default
	// 30s. Tests can shrink it to keep the suite snappy.
	BackoffBase time.Duration

	// OnEnqueue, if set, is called once per newly-inserted URL
	// after the row commits. Frame events are wired by the caller
	// (apps/crawler, apps/frontier-worker) so pkg/frontier stays
	// transport-agnostic. Errors are logged by the caller; the
	// frontier doesn't retry the emit.
	OnEnqueue func(ctx context.Context, u URL)
}

// NewPostgresFrontier wires a Frontier against the supplied GORM
// pool factory.
func NewPostgresFrontier(db PoolFn) *PostgresFrontier {
	return &PostgresFrontier{
		db:          db,
		MaxBackoff:  1 * time.Hour,
		BackoffBase: 30 * time.Second,
	}
}

// row is the GORM-mapped struct for url_frontier. Kept private —
// callers interact with frontier.URL.
type row struct {
	URLID            string     `gorm:"column:url_id;primaryKey"`
	CanonicalURL     string     `gorm:"column:canonical_url"`
	CanonicalURLHash string     `gorm:"column:canonical_url_hash"`
	Host             string     `gorm:"column:host"`
	SourceID         string     `gorm:"column:source_id"`
	Priority         float64    `gorm:"column:priority"`
	State            string     `gorm:"column:state"`
	Attempts         int        `gorm:"column:attempts"`
	LastError        string     `gorm:"column:last_error"`
	EnqueuedAt       time.Time  `gorm:"column:enqueued_at"`
	ClaimedAt        *time.Time `gorm:"column:claimed_at"`
	ClaimedBy        string     `gorm:"column:claimed_by"`
	CompletedAt      *time.Time `gorm:"column:completed_at"`
	NextAttemptAt    *time.Time `gorm:"column:next_attempt_at"`
	Metadata         []byte     `gorm:"column:metadata;type:jsonb"`
}

// TableName binds row → url_frontier so GORM doesn't pluralise.
func (row) TableName() string { return "url_frontier" }

// hostStateRow mirrors host_state for the dequeue UPDATE inside
// the txn. We never SELECT it through GORM; this struct exists so
// AutoMigrate in tests can shape the table.
type hostStateRow struct {
	Host            string     `gorm:"column:host;primaryKey"`
	WindowMinutes   int        `gorm:"column:window_minutes"`
	LastRequestAt   *time.Time `gorm:"column:last_request_at"`
	NextEligibleAt  *time.Time `gorm:"column:next_eligible_at"`
	OkCount24H      int        `gorm:"column:ok_count_24h"`
	ErrCount24H     int        `gorm:"column:err_count_24h"`
	ConcurrencyMax  int        `gorm:"column:concurrency_max"`
	ConcurrencyNow  int        `gorm:"column:concurrency_now"`
}

// TableName binds hostStateRow → host_state.
func (hostStateRow) TableName() string { return "host_state" }

// Schema returns the slice of models AutoMigrate needs to shape
// the queue + politeness tables. Used by integration tests; in
// production the SQL migrations are the source of truth.
func Schema() []any {
	return []any{&row{}, &hostStateRow{}}
}

// hashCanonical is the dedup key. sha256 over the canonicalized URL
// (lower-cased scheme+host, tracking params stripped, query sorted,
// fragment + trailing slash removed) so URLs that differ only by those
// cosmetic facets dedup to a single frontier row. The migration enforces
// uniqueness so a race between two Enqueue calls always lands at most
// one row. Callers must pass the already-canonicalized URL.
func hashCanonical(canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// ensureHost upserts a host_state row so the Dequeue JOIN always
// finds the host. Idempotent — duplicate inserts fall through.
func ensureHost(tx *gorm.DB, host string) error {
	hs := hostStateRow{
		Host:           host,
		WindowMinutes:  1,
		ConcurrencyMax: 1,
	}
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "host"}},
		DoNothing: true,
	}).Create(&hs).Error
}

// Enqueue inserts each URL, dedup'ing on canonical_url_hash. For
// duplicates we keep the higher of (existing.priority, new.priority)
// and leave state untouched. Per-row validation failures don't fail
// the whole batch — the returned slice carries the IDs of every row
// that successfully made it through (insert or merge), and the err
// is non-nil if any row was rejected.
func (f *PostgresFrontier) Enqueue(ctx context.Context, urls []URL) ([]EnqueueResult, error) {
	if len(urls) == 0 {
		return nil, nil
	}
	results := make([]EnqueueResult, 0, len(urls))
	var firstErr error

	db := f.db(ctx, false)
	for _, u := range urls {
		// Canonicalize so the stored canonical_url and its dedup hash
		// are the normalized form regardless of what the caller passed.
		canonical := CanonicalizeURL(u.CanonicalURL)
		host := u.Host
		if host == "" {
			host = HostOf(canonical)
		}
		if canonical == "" || host == "" {
			if firstErr == nil {
				firstErr = fmt.Errorf("%w: %q", ErrInvalidURL, canonical)
			}
			continue
		}

		id := u.URLID
		if id == "" {
			id = xid.New().String()
		}
		metadata := u.Metadata
		if len(metadata) == 0 {
			metadata = []byte("{}")
		}

		hash := hashCanonical(canonical)
		newRow := row{
			URLID:            id,
			CanonicalURL:     canonical,
			CanonicalURLHash: hash,
			Host:             host,
			SourceID:         u.SourceID,
			Priority:         u.Priority,
			State:            string(StatePending),
			Metadata:         metadata,
		}

		var (
			inserted    bool
			persistedID string
		)
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := ensureHost(tx, host); err != nil {
				return err
			}

			// INSERT ... ON CONFLICT DO NOTHING in a single shot.
			// Postgres aborts the surrounding txn on a plain
			// constraint violation (SQLSTATE 25P02), so we
			// cannot use a bare INSERT + fallback SELECT — the
			// fallback SELECT would fail with "current
			// transaction is aborted". ON CONFLICT keeps the
			// txn healthy and lets us inspect RowsAffected to
			// branch into the priority-merge path.
			ins := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "canonical_url_hash"}},
				DoNothing: true,
			}).Create(&newRow)
			if ins.Error != nil {
				return ins.Error
			}
			if ins.RowsAffected == 1 {
				inserted = true
				persistedID = newRow.URLID
				return nil
			}

			// Duplicate path: look up the existing row + maybe
			// bump priority. State stays untouched so in-flight
			// URLs aren't reset by a re-discovery.
			var existing row
			if err := tx.Where("canonical_url_hash = ?", hash).First(&existing).Error; err != nil {
				return err
			}
			persistedID = existing.URLID
			if u.Priority > existing.Priority {
				if err := tx.Model(&row{}).
					Where("url_id = ?", existing.URLID).
					Update("priority", u.Priority).Error; err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}

		results = append(results, EnqueueResult{
			URLID:    persistedID,
			Inserted: inserted,
		})

		// Wake-up signal — only for net-new rows. Re-enqueues of an
		// existing canonical URL just bump priority; emitting on
		// those would flood the worker with no-op nudges.
		if inserted && f.OnEnqueue != nil {
			f.OnEnqueue(ctx, URL{
				URLID:        persistedID,
				CanonicalURL: canonical,
				Host:         host,
				SourceID:     u.SourceID,
				Priority:     u.Priority,
				State:        StatePending,
				EnqueuedAt:   time.Now().UTC(),
				Metadata:     metadata,
			})
		}
	}
	return results, firstErr
}

// Two-phase claim:
//   1. Lock at most one host_state row per call with FOR UPDATE
//      SKIP LOCKED. The host row is the politeness mutex; once
//      one worker holds it, others skip the host until commit.
//      Limiting to N hosts at a time is intentional — each call
//      claims at most N URLs anyway, one per host.
//   2. For each locked host, take the top-priority pending URL
//      with FOR UPDATE SKIP LOCKED. The double SKIP LOCKED is
//      belt-and-braces: in steady state phase 1 alone keeps
//      different workers on different hosts.
//
// We can't express this in a single SELECT — Postgres requires
// FOR UPDATE on a single table, not on an IN/EXISTS subquery,
// when SKIP LOCKED is involved. Splitting the lock acquisition
// into two queries keeps SKIP LOCKED honest and avoids the
// "DISTINCT ON ignores SKIP LOCKED" trap.
const dequeueLockHostsSQL = `
SELECT h.host
FROM   host_state h
WHERE  (h.next_eligible_at IS NULL OR h.next_eligible_at <= now())
  AND  h.concurrency_now < h.concurrency_max
  AND  EXISTS (
       SELECT 1 FROM url_frontier f
        WHERE f.host = h.host
          AND f.state = 'pending'
          AND (f.next_attempt_at IS NULL OR f.next_attempt_at <= now())
       )
ORDER BY (
    SELECT max(f.priority) FROM url_frontier f
     WHERE f.host = h.host
       AND f.state = 'pending'
       AND (f.next_attempt_at IS NULL OR f.next_attempt_at <= now())
   ) DESC NULLS LAST
LIMIT  ?
FOR UPDATE OF h SKIP LOCKED
`

const dequeuePickURLSQL = `
SELECT f.url_id, f.canonical_url, f.host, f.source_id, f.priority,
       f.state, f.attempts, f.enqueued_at, f.metadata
FROM   url_frontier f
WHERE  f.host = ?
  AND  f.state = 'pending'
  AND  (f.next_attempt_at IS NULL OR f.next_attempt_at <= now())
ORDER BY f.priority DESC, f.enqueued_at ASC
LIMIT  1
FOR UPDATE OF f SKIP LOCKED
`

// staleLeaseSeconds is the in_flight lease: a claim older than this is
// treated as abandoned (worker crashed) and reclaimed. It must be safely
// greater than the worker's per-URL deadline (6m: httpx retries capped at
// ~90s + ~5m inference + overhead) so a live-but-slow worker always
// finishes before its claim is reclaimed — reclaiming a live claim would
// double-process the URL. 15m gives comfortable margin over the 6m
// per-URL deadline: per-URL-deadline (6m) < lease (15m).
const staleLeaseSeconds = 900

// reclaimStaleSQL recovers work abandoned by a crashed/redeployed
// worker. A claim is a lease: the worker sets state='in_flight' +
// claimed_at and bumps host_state.concurrency_now. If the pod dies
// before Complete/Fail (e.g. a rolling redeploy mid-fetch), the row
// stays in_flight forever AND concurrency_now stays incremented —
// which, at concurrency_max=1, permanently deadlocks every future
// Dequeue for that host. This resets such rows to pending and
// decrements each host's counter by the number reclaimed, all in one
// statement (a data-modifying CTE feeding the host_state UPDATE).
const reclaimStaleSQL = `
WITH reclaimed AS (
    UPDATE url_frontier
       SET state = 'pending', claimed_by = NULL, claimed_at = NULL
     WHERE state = 'in_flight'
       AND claimed_at < now() - make_interval(secs => ?)
    RETURNING host
), counts AS (
    SELECT host, count(*) AS n FROM reclaimed GROUP BY host
)
UPDATE host_state h
   SET concurrency_now = GREATEST(h.concurrency_now - c.n, 0)
  FROM counts c
 WHERE h.host = c.host
`

// Dequeue runs the FOR UPDATE SKIP LOCKED claim + the in-txn
// state-flip in one transaction. Returns the URLs claimed.
//
// Empty result + nil error means "queue empty / all eligible
// rows already in-flight" — the worker loop should idle.
func (f *PostgresFrontier) Dequeue(ctx context.Context, n int, workerID string) ([]URL, error) {
	if n <= 0 {
		return nil, nil
	}
	out := make([]URL, 0, n)

	// Reclaim leases abandoned by crashed/redeployed workers before
	// claiming new work — otherwise a stuck in_flight row + its
	// concurrency_now increment deadlock the host. Lease is 15m:
	// comfortably longer than the worker's 6m per-URL deadline so we
	// never reclaim a URL a live worker is still handling.
	if err := f.db(ctx, false).
		Session(&gorm.Session{PrepareStmt: false}).
		Exec(reclaimStaleSQL, staleLeaseSeconds).Error; err != nil {
		return nil, fmt.Errorf("frontier reclaim stale: %w", err)
	}

	err := f.db(ctx, false).
		Session(&gorm.Session{PrepareStmt: false}).
		Transaction(func(tx *gorm.DB) error {
			// Phase 1 — lock up to n eligible host rows. Other
			// workers running concurrently will see those rows
			// as locked and skip them (SKIP LOCKED).
			var hostsLocked []string
			if err := tx.Raw(dequeueLockHostsSQL, n).Scan(&hostsLocked).Error; err != nil {
				return fmt.Errorf("frontier dequeue host-lock: %w", err)
			}
			if len(hostsLocked) == 0 {
				return nil
			}

			// Phase 2 — for each locked host, claim the
			// top-priority pending URL. FOR UPDATE OF f
			// SKIP LOCKED here is belt-and-braces; the host
			// lock has already excluded other workers.
			picks := make([]row, 0, len(hostsLocked))
			for _, h := range hostsLocked {
				var r row
				err := tx.Raw(dequeuePickURLSQL, h).Scan(&r).Error
				if err != nil {
					return fmt.Errorf("frontier dequeue pick url: %w", err)
				}
				if r.URLID == "" {
					// Host has no eligible URL (race with
					// another worker that cleared the host's
					// pending row between phases). Skip.
					continue
				}
				picks = append(picks, r)
			}
			if len(picks) == 0 {
				return nil
			}

			ids := make([]string, 0, len(picks))
			hosts := map[string]struct{}{}
			for _, r := range picks {
				ids = append(ids, r.URLID)
				hosts[r.Host] = struct{}{}
			}

			// Flip the rows to in_flight, bump attempts, and
			// stamp ownership. attempts is incremented here so the
			// counter reflects "this is the Nth claim attempt"
			// regardless of whether the worker succeeds.
			now := time.Now().UTC()
			if err := tx.Model(&row{}).
				Where("url_id IN ?", ids).
				Updates(map[string]any{
					"state":      string(StateInFlight),
					"claimed_at": now,
					"claimed_by": workerID,
					"attempts":   gorm.Expr("attempts + 1"),
				}).Error; err != nil {
				return fmt.Errorf("frontier dequeue flip: %w", err)
			}

			// Advance host_state for every claimed host. One row per
			// host even if many URLs share it — the politeness gate
			// is per-host, not per-URL.
			for h := range hosts {
				if err := tx.Exec(`
					UPDATE host_state
					   SET concurrency_now  = concurrency_now + 1,
					       last_request_at  = now(),
					       next_eligible_at = now() + (window_minutes || ' minutes')::interval
					 WHERE host = ?`, h).Error; err != nil {
					return fmt.Errorf("frontier dequeue host_state: %w", err)
				}
			}

			for _, r := range picks {
				claimedAt := now
				out = append(out, URL{
					URLID:        r.URLID,
					CanonicalURL: r.CanonicalURL,
					Host:         r.Host,
					SourceID:     r.SourceID,
					Priority:     r.Priority,
					State:        StateInFlight,
					Attempts:     r.Attempts + 1,
					EnqueuedAt:   r.EnqueuedAt,
					ClaimedAt:    &claimedAt,
					Metadata:     r.Metadata,
				})
			}
			return nil
		})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Complete marks the URL done + bumps the per-host success
// counter + frees the concurrency slot. Best-effort host_state
// update — a missing host_state row should not fail Complete.
func (f *PostgresFrontier) Complete(ctx context.Context, urlID string) error {
	if urlID == "" {
		return errors.New("frontier complete: empty url_id")
	}
	return f.db(ctx, false).Transaction(func(tx *gorm.DB) error {
		var r row
		if err := tx.Where("url_id = ?", urlID).First(&r).Error; err != nil {
			return fmt.Errorf("frontier complete load: %w", err)
		}
		now := time.Now().UTC()
		if err := tx.Model(&row{}).
			Where("url_id = ?", urlID).
			Updates(map[string]any{
				"state":        string(StateDone),
				"completed_at": now,
				"last_error":   "",
			}).Error; err != nil {
			return fmt.Errorf("frontier complete flip: %w", err)
		}
		if r.Host != "" {
			if err := tx.Exec(`
				UPDATE host_state
				   SET concurrency_now = GREATEST(concurrency_now - 1, 0),
				       ok_count_24h   = ok_count_24h + 1
				 WHERE host = ?`, r.Host).Error; err != nil {
				return fmt.Errorf("frontier complete host_state: %w", err)
			}
		}
		return nil
	})
}

// Fail records the failure. When attempts < maxAttempts the URL
// returns to state='pending' with next_attempt_at = now() + backoff
// (exponential, capped at MaxBackoff). At attempts == maxAttempts
// the row transitions to state='failed' permanently.
func (f *PostgresFrontier) Fail(ctx context.Context, urlID string, runErr error, maxAttempts int) error {
	if urlID == "" {
		return errors.New("frontier fail: empty url_id")
	}
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	errMsg := ""
	if runErr != nil {
		errMsg = runErr.Error()
	}

	return f.db(ctx, false).Transaction(func(tx *gorm.DB) error {
		var r row
		if err := tx.Where("url_id = ?", urlID).First(&r).Error; err != nil {
			return fmt.Errorf("frontier fail load: %w", err)
		}

		updates := map[string]any{
			"last_error": errMsg,
		}
		if r.Attempts >= maxAttempts {
			updates["state"] = string(StateFailed)
		} else {
			// Exponential backoff: 2^(attempts-1) * BackoffBase,
			// capped at MaxBackoff. attempts has already been
			// incremented by Dequeue, so attempts-1 indexes the
			// retry count correctly.
			exp := math.Pow(2, float64(r.Attempts-1))
			delay := time.Duration(exp) * f.BackoffBase
			if delay > f.MaxBackoff || delay <= 0 {
				delay = f.MaxBackoff
			}
			nextAt := time.Now().UTC().Add(delay)
			updates["state"] = string(StatePending)
			updates["next_attempt_at"] = nextAt
		}

		if err := tx.Model(&row{}).Where("url_id = ?", urlID).Updates(updates).Error; err != nil {
			return fmt.Errorf("frontier fail flip: %w", err)
		}
		if r.Host != "" {
			if err := tx.Exec(`
				UPDATE host_state
				   SET concurrency_now = GREATEST(concurrency_now - 1, 0),
				       err_count_24h  = err_count_24h + 1
				 WHERE host = ?`, r.Host).Error; err != nil {
				return fmt.Errorf("frontier fail host_state: %w", err)
			}
		}
		return nil
	})
}
