package jobqueue

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/rs/xid"
	"gorm.io/gorm"
)

// Catalog command kinds and outcomes.
const (
	CommandDeactivateSource = "deactivate_source"

	// sourceStatusDisabled mirrors domain.SourceDisabled (operator stop).
	// Duplicated as a literal to keep jobqueue free of a domain import cycle.
	sourceStatusDisabled = "disabled"
)

// CommandOutcome reports what RunCatalogCommand did with a claimed command.
type CommandOutcome string

const (
	// CommandApplied: the catalog change was made.
	CommandApplied CommandOutcome = "applied"
	// CommandSuperseded: the source is no longer stopped; nothing to do.
	CommandSuperseded CommandOutcome = "superseded"
	// CommandDeferred: ingest work for the source is still in flight; the
	// command was put back and will be retried shortly.
	CommandDeferred CommandOutcome = "deferred"
	// CommandLostLease: another worker took the command over; this worker's
	// result was discarded.
	CommandLostLease CommandOutcome = "lost_lease"
)

// CatalogCommand is a claimed row of catalog_commands.
type CatalogCommand struct {
	ID, Kind, SourceID, Reason, ClaimedBy string
	Attempt                               int
	CreatedAt                             time.Time
	// Followup marks the one delayed re-sweep scheduled after an applied
	// deactivation (it does not schedule another).
	Followup bool
}

// commandDeferDelay is how long a command waits when ingest work for its
// source is still being processed.
const commandDeferDelay = 30 * time.Second

// minFollowupDelay is the floor for the post-deactivation sweep. The delay
// must exceed the ingest lease plus commit time so the sweep runs after any
// stalled writer has finished; see Store.followupDelay.
const minFollowupDelay = 10 * time.Minute

// WithIngestLease tells the store the worker's ingest lease so the follow-up
// sweep is scheduled comfortably after any lease-expired writer.
func (s *Store) WithIngestLease(lease time.Duration) *Store {
	if s != nil {
		s.ingestLease = lease
	}
	return s
}

func (s *Store) followupDelay() time.Duration {
	if d := 3 * s.ingestLease; d > minFollowupDelay {
		return d
	}
	return minFollowupDelay
}

// RequestSourceDeactivation records (on the crawl database) that every
// catalog listing of sourceID must be hidden. Safe to call repeatedly.
func (s *Store) RequestSourceDeactivation(ctx context.Context, sourceID, reason string) error {
	if s == nil || s.db == nil {
		return errors.New("jobqueue: database is not configured")
	}
	if sourceID == "" {
		return errors.New("jobqueue: source id is required")
	}
	// One pending request per source is enough: it re-reads the source and
	// sweeps everything when it runs. Avoids a pile of redundant commands
	// when many late writes re-request the same deactivation.
	return s.db(ctx, false).WithContext(ctx).Exec(`INSERT INTO catalog_commands
		(id, kind, source_id, reason)
		SELECT ?, ?, ?, ?
		WHERE NOT EXISTS (SELECT 1 FROM catalog_commands
			WHERE source_id = ? AND kind = ? AND status = 'pending' AND NOT followup)`,
		xid.New().String(), CommandDeactivateSource, sourceID, reason,
		sourceID, CommandDeactivateSource).Error
}

// ClaimCatalogCommand leases the oldest runnable command, or returns nil when
// there is none. Expired leases (crashed worker) are reclaimed.
func (s *Store) ClaimCatalogCommand(ctx context.Context, owner string, lease time.Duration) (*CatalogCommand, error) {
	if lease <= 0 {
		lease = 2 * time.Minute
	}
	var rows []CatalogCommand
	err := s.db(ctx, false).WithContext(ctx).Raw(`WITH picked AS (
		SELECT id FROM catalog_commands
		WHERE (status = 'pending' AND available_at <= now())
		   OR (status = 'processing' AND lease_expires_at < now())
		ORDER BY available_at, created_at
		LIMIT 1 FOR UPDATE SKIP LOCKED
	)
	UPDATE catalog_commands c SET status='processing', attempt=c.attempt+1,
		lease_expires_at=now()+make_interval(secs => ?), claimed_by=?, updated_at=now()
	FROM picked WHERE c.id = picked.id
	RETURNING c.id, c.kind, c.source_id, c.reason, c.claimed_by, c.attempt, c.followup, c.created_at`,
		lease.Seconds(), owner).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("claim catalog command: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// RunCatalogCommand applies a claimed command. Order matters for
// deactivate_source:
//  1. if the operator restarted the source since, the command is superseded;
//  2. while any ingest item of the source is mid-processing, defer — such an
//     item may have passed the stopped-source check before the stop and would
//     re-show listings if it committed after the deactivation;
//  3. hide the source's listings on the product database;
//  4. mark the command done, fenced on this worker still owning the lease.
//
// Items claimed after the stop are discarded by the worker (SourceStopped),
// so once step 2 passes no further write can resurrect the listings.
func (s *Store) RunCatalogCommand(ctx context.Context, cmd CatalogCommand) (CommandOutcome, error) {
	if cmd.Kind != CommandDeactivateSource {
		return "", fmt.Errorf("jobqueue: unknown catalog command kind %q", cmd.Kind)
	}
	stopped, err := s.SourceStopped(ctx, cmd.SourceID)
	if err != nil {
		return "", err
	}
	if !stopped {
		return s.finishCommand(ctx, cmd, "superseded", CommandSuperseded)
	}

	// Only items claimed before this command was created can have passed the
	// stopped-source check while the source was still active. Items claimed
	// later always see it stopped and are discarded, so they must not hold
	// the deactivation back (a large backlog would otherwise defer it until
	// the whole backlog drained). A reclaim resets claimed_at; a stale owner
	// of such an item is covered by the post-write re-check and the sweep.
	var inFlight int64
	if err := s.db(ctx, false).WithContext(ctx).Raw(
		`SELECT count(*) FROM job_ingest_queue
		 WHERE source_id = ? AND status = 'processing' AND claimed_at < ?`,
		cmd.SourceID, cmd.CreatedAt).Scan(&inFlight).Error; err != nil {
		return "", fmt.Errorf("count in-flight ingest: %w", err)
	}
	if inFlight > 0 {
		res := s.db(ctx, false).WithContext(ctx).Exec(`UPDATE catalog_commands
			SET status='pending', attempt=attempt-1, available_at=now()+make_interval(secs => ?),
			    lease_expires_at=NULL, claimed_by=NULL, updated_at=now()
			WHERE id=? AND claimed_by=? AND status='processing'`,
			commandDeferDelay.Seconds(), cmd.ID, cmd.ClaimedBy)
		if res.Error != nil {
			return "", fmt.Errorf("defer catalog command: %w", res.Error)
		}
		if res.RowsAffected != 1 {
			return CommandLostLease, nil
		}
		return CommandDeferred, nil
	}

	if err := s.DeactivateSource(ctx, cmd.SourceID, cmd.Reason); err != nil {
		return "", err
	}
	outcome, err := s.finishCommand(ctx, cmd, "done", CommandApplied)
	if err != nil || outcome != CommandApplied || cmd.Followup {
		return outcome, err
	}
	// Only the owner that completed the command schedules the sweep. A
	// worker that stalled past its lease can still commit a catalog write
	// after this point (and crash before its own re-check), so sweep once
	// more after such writes must have finished.
	if err := s.db(ctx, false).WithContext(ctx).Exec(`INSERT INTO catalog_commands
		(id, kind, source_id, reason, followup, available_at)
		VALUES (?, ?, ?, ?, true, now()+make_interval(secs => ?))`,
		xid.New().String(), cmd.Kind, cmd.SourceID, cmd.Reason, s.followupDelay().Seconds()).Error; err != nil {
		return CommandApplied, fmt.Errorf("deactivation applied; scheduling follow-up sweep failed: %w", err)
	}
	return CommandApplied, nil
}

// CommandBacklog reports catalog commands needing attention: dead ones, and
// pending ones that have waited longer than staleAfter.
func (s *Store) CommandBacklog(ctx context.Context, staleAfter time.Duration) (dead, stale int64, err error) {
	var row struct{ Dead, Stale int64 }
	err = s.db(ctx, true).WithContext(ctx).Raw(`SELECT
		count(*) FILTER (WHERE status = 'dead') AS dead,
		count(*) FILTER (WHERE status IN ('pending','processing') AND created_at < now() - make_interval(secs => ?)) AS stale
		FROM catalog_commands WHERE status IN ('dead','pending','processing')`, staleAfter.Seconds()).Scan(&row).Error
	return row.Dead, row.Stale, err
}

func (s *Store) finishCommand(ctx context.Context, cmd CatalogCommand, status string, outcome CommandOutcome) (CommandOutcome, error) {
	res := s.db(ctx, false).WithContext(ctx).Exec(`UPDATE catalog_commands
		SET status=?, processed_at=now(), lease_expires_at=NULL, last_error=NULL, updated_at=now()
		WHERE id=? AND claimed_by=? AND status='processing'`, status, cmd.ID, cmd.ClaimedBy)
	if res.Error != nil {
		return "", fmt.Errorf("finish catalog command: %w", res.Error)
	}
	if res.RowsAffected != 1 {
		return CommandLostLease, nil
	}
	return outcome, nil
}

// FailCatalogCommand records a failed attempt: retried with backoff, dead
// after maxAttempts. Fenced on the claim so a stale worker cannot overwrite
// a newer owner's state.
func (s *Store) FailCatalogCommand(ctx context.Context, cmd CatalogCommand, cause error, maxAttempts int) error {
	if maxAttempts <= 0 {
		maxAttempts = 20
	}
	status := "pending"
	if cmd.Attempt >= maxAttempts {
		status = "dead"
	}
	delay := math.Min(math.Pow(2, float64(cmd.Attempt)), 900)
	msg := cause.Error()
	if len(msg) > 2000 {
		msg = msg[:2000]
	}
	return s.db(ctx, false).WithContext(ctx).Exec(`UPDATE catalog_commands
		SET status=?, available_at=now()+make_interval(secs => ?), last_error=?,
		    lease_expires_at=NULL, claimed_by=NULL, updated_at=now()
		WHERE id=? AND claimed_by=? AND status='processing'`,
		status, delay, msg, cmd.ID, cmd.ClaimedBy).Error
}

// SourceStopped reports whether the crawl-plane source has been stopped by an
// operator (status 'disabled'). An unknown source is not stopped.
func (s *Store) SourceStopped(ctx context.Context, sourceID string) (bool, error) {
	if sourceID == "" {
		return false, nil
	}
	var status []string
	if err := s.db(ctx, false).WithContext(ctx).Raw(
		`SELECT status FROM sources WHERE id = ?`, sourceID).Scan(&status).Error; err != nil {
		return false, fmt.Errorf("load source status: %w", err)
	}
	return len(status) == 1 && status[0] == sourceStatusDisabled, nil
}

// Discard terminally drops a claimed item without touching the catalog (its
// source was stopped). Fenced on the item still being processing.
func (s *Store) Discard(ctx context.Context, item Item, reason string) error {
	return s.db(ctx, false).WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Exec(`UPDATE job_ingest_queue SET status='rejected', last_error=?,
			lease_expires_at=NULL, claimed_by=NULL, updated_at=now()
			WHERE id=? AND status='processing' AND claimed_by=?`, reason, item.ID, item.ClaimedBy)
		if res.Error != nil {
			return fmt.Errorf("discard queue item: %w", res.Error)
		}
		if res.RowsAffected != 1 {
			return ErrLostLease
		}
		return appendEvent(tx, item.ID, item.VariantID, item.SourceID, "discarded", item.Attempt,
			fmt.Sprintf(`{"reason":%q}`, reason))
	})
}
