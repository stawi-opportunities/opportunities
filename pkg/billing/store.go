package billing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// ErrNotFound is returned when a checkout row doesn't exist.
var ErrNotFound = errors.New("billing: checkout not found")

// Checkout is one row of the candidate_checkouts ledger (migration 0020).
type Checkout struct {
	PromptID       string
	CandidateID    string
	PlanID         string
	Route          string
	Status         Status
	SubscriptionID string
	AmountCents    int64
	Currency       string
	Country        string
	RedirectURL    string
	Error          string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	// ActivatedAt is set once the paid checkout's entitlement was granted.
	// nil on a paid row means activation failed and must be retried.
	ActivatedAt *time.Time
	// PaidAt is when the checkout first moved to paid.
	PaidAt *time.Time
}

// Store is the raw-SQL persistence for candidate_checkouts. Same style as
// pkg/savedjobs.Store — the queries are aggregate-shaped and don't fit
// datastore.BaseRepository.
type Store struct {
	db *sql.DB
}

// NewStore wraps a *sql.DB. nil is tolerated by callers that gate on it.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// Create inserts a pending checkout. prompt_id is the natural key; a
// duplicate prompt_id is an idempotent no-op error the caller can ignore.
func (s *Store) Create(ctx context.Context, c Checkout) error {
	if c.Status == "" {
		c.Status = StatusPending
	}
	const q = `
INSERT INTO candidate_checkouts
    (prompt_id, candidate_id, plan_id, route, status, subscription_id,
     amount_cents, currency, country, redirect_url, error)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (prompt_id) DO NOTHING
`
	if _, err := s.db.ExecContext(ctx, q,
		c.PromptID, c.CandidateID, c.PlanID, c.Route, string(c.Status), c.SubscriptionID,
		c.AmountCents, c.Currency, c.Country, c.RedirectURL, c.Error,
	); err != nil {
		return fmt.Errorf("billing: create checkout: %w", err)
	}
	return nil
}

// GetByPromptID returns the checkout for promptID, or ErrNotFound.
func (s *Store) GetByPromptID(ctx context.Context, promptID string) (Checkout, error) {
	const q = `
SELECT prompt_id, candidate_id, plan_id, route, status, subscription_id,
       amount_cents, currency, country, redirect_url, error, created_at, updated_at,
       activated_at, paid_at
FROM candidate_checkouts
WHERE prompt_id = $1
`
	return s.scanOne(s.db.QueryRowContext(ctx, q, promptID))
}

// CanTransition reports whether a checkout may move from -> to. Statuses only
// move forward: a non-terminal checkout resolves to paid or failed, and a
// failed one may still become paid (a late successful charge is real money).
// A paid checkout never changes again, so replayed or out-of-order provider
// notifications cannot re-grant access or overwrite a payment.
func CanTransition(from, to Status) bool {
	switch to {
	case StatusPaid:
		return from == StatusPending || from == StatusRedirect || from == StatusFailed
	case StatusFailed:
		return from == StatusPending || from == StatusRedirect
	default:
		return false
	}
}

// ResolveStatus applies a terminal status to the checkout if CanTransition
// allows it. changed reports whether this call moved the row; when false the
// current row is returned untouched (already resolved). ErrNotFound when the
// prompt is unknown.
func (s *Store) ResolveStatus(ctx context.Context, promptID string, status Status, subscriptionID, errMsg string) (Checkout, bool, error) {
	const q = `
UPDATE candidate_checkouts
SET status = $2,
    subscription_id = CASE WHEN $3 <> '' THEN $3 ELSE subscription_id END,
    error = $4,
    paid_at = CASE WHEN $2 = 'paid' THEN COALESCE(paid_at, now()) ELSE paid_at END,
    updated_at = now()
WHERE prompt_id = $1
  AND (status IN ('pending', 'redirect') OR (status = 'failed' AND $2 = 'paid'))
  AND $2 IN ('paid', 'failed')
RETURNING prompt_id, candidate_id, plan_id, route, status, subscription_id,
          amount_cents, currency, country, redirect_url, error, created_at, updated_at,
          activated_at, paid_at
`
	row, err := s.scanOne(s.db.QueryRowContext(ctx, q, promptID, string(status), subscriptionID, errMsg))
	if err == nil {
		return row, true, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Checkout{}, false, err
	}
	// No row moved: either the prompt is unknown or it is already resolved.
	cur, getErr := s.GetByPromptID(ctx, promptID)
	if getErr != nil {
		return Checkout{}, false, getErr
	}
	return cur, false, nil
}

// MaxActivationAttempts bounds how often a paid checkout's activation is
// claimed before the reconciler stops retrying it (it is then counted as
// stuck for operators).
const MaxActivationAttempts = 10

// ActivationLease is how long an activation claim is honoured before another
// caller may take over (a crashed or hung activator).
const ActivationLease = 10 * time.Minute

// ActivationClaim is the token of an activation claim (its timestamp);
// release is fenced on it so a holder that outlived its lease cannot clear a
// newer holder's claim.
type ActivationClaim struct{ At time.Time }

// ClaimActivation takes the exclusive right to grant a paid checkout's
// entitlement. ok=false means it is already activated, another caller holds a
// live claim, it is waiting out a retry backoff, or it exhausted its
// attempts — the caller must not run side effects.
func (s *Store) ClaimActivation(ctx context.Context, promptID string) (ActivationClaim, bool, error) {
	const q = `
UPDATE candidate_checkouts
SET activation_claimed_at = clock_timestamp(),
    activation_attempts = activation_attempts + 1,
    updated_at = now()
WHERE prompt_id = $1
  AND status = 'paid'
  AND activated_at IS NULL
  AND activation_attempts < $3
  AND (next_activation_at IS NULL OR next_activation_at <= now())
  AND (activation_claimed_at IS NULL OR activation_claimed_at < now() - make_interval(secs => $2))
RETURNING activation_claimed_at
`
	var at time.Time
	err := s.db.QueryRowContext(ctx, q, promptID, ActivationLease.Seconds(), MaxActivationAttempts).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return ActivationClaim{}, false, nil
	}
	if err != nil {
		return ActivationClaim{}, false, fmt.Errorf("billing: claim activation: %w", err)
	}
	return ActivationClaim{At: at}, true, nil
}

// ReleaseActivation gives up a claim after a failed activation and schedules
// the next retry with exponential backoff (30s doubling, capped at 6h).
func (s *Store) ReleaseActivation(ctx context.Context, promptID string, claim ActivationClaim, cause error) error {
	msg := ""
	if cause != nil {
		msg = cause.Error()
	}
	if len(msg) > 2000 {
		msg = msg[:2000]
	}
	const q = `
UPDATE candidate_checkouts
SET activation_claimed_at = NULL,
    next_activation_at = now() + make_interval(secs => LEAST(30 * power(2, GREATEST(activation_attempts - 1, 0)), 21600)),
    activation_error = $2,
    updated_at = now()
WHERE prompt_id = $1 AND activated_at IS NULL AND activation_claimed_at = $3
`
	if _, err := s.db.ExecContext(ctx, q, promptID, msg, claim.At); err != nil {
		return fmt.Errorf("billing: release activation: %w", err)
	}
	return nil
}

// Activation outcomes recorded by MarkActivated.
const (
	OutcomeApplied     = "applied"
	OutcomeSuperseded  = "superseded"
	OutcomeAlreadyHeld = "already_held"
)

// MarkActivated records that the paid checkout was handled (outcome) and
// clears the claim. Idempotent: the first timestamp and outcome win.
func (s *Store) MarkActivated(ctx context.Context, promptID, outcome string) error {
	const q = `
UPDATE candidate_checkouts
SET activated_at = COALESCE(activated_at, now()),
    activation_outcome = CASE WHEN activation_outcome = '' THEN $2 ELSE activation_outcome END,
    activation_claimed_at = NULL,
    activation_error = '',
    updated_at = now()
WHERE prompt_id = $1`
	if _, err := s.db.ExecContext(ctx, q, promptID, outcome); err != nil {
		return fmt.Errorf("billing: mark activated: %w", err)
	}
	return nil
}

// HasNewerActivatedSubscription reports whether another subscription checkout
// of the candidate that was paid after c is already active — i.e. the user's
// later purchase is live, so applying c (paid earlier) would overwrite it.
// Payment order decides on both sides (not checkout creation, and not
// activation order, which retries can reverse). paid_at is when we learned
// of the payment (the provider reports no payment time); rows without it
// fall back to their activation time.
func (s *Store) HasNewerActivatedSubscription(ctx context.Context, c Checkout) (bool, error) {
	oneTime := make([]string, 0, len(oneTimeProducts))
	for _, p := range oneTimeProducts {
		oneTime = append(oneTime, string(p.ID))
	}
	paidAt := c.CreatedAt
	if c.PaidAt != nil {
		paidAt = *c.PaidAt
	}
	const q = `
SELECT EXISTS (
  SELECT 1 FROM candidate_checkouts n
  WHERE n.candidate_id = $1
    AND n.prompt_id <> $2
    AND n.activated_at IS NOT NULL
    AND n.activation_outcome <> 'superseded'
    AND COALESCE(n.paid_at, n.activated_at) > $3
    AND NOT (n.plan_id = ANY($4::text[]))
)`
	var newer bool
	if err := s.db.QueryRowContext(ctx, q, c.CandidateID, c.PromptID, paidAt, pq.Array(oneTime)).Scan(&newer); err != nil {
		return false, fmt.Errorf("billing: newer activated subscription: %w", err)
	}
	return newer, nil
}

// CountSupersededSince counts paid checkouts not applied because a newer
// purchase was already active (money taken, nothing granted) since t.
func (s *Store) CountSupersededSince(ctx context.Context, t time.Time) (int64, error) {
	const q = `
SELECT count(*) FROM candidate_checkouts
WHERE status = 'paid' AND activation_outcome = 'superseded' AND activated_at >= $1`
	var n int64
	if err := s.db.QueryRowContext(ctx, q, t).Scan(&n); err != nil {
		return 0, fmt.Errorf("billing: count superseded: %w", err)
	}
	return n, nil
}

// CountStuckActivations counts paid checkouts whose activation exhausted its
// retries. Non-zero needs an operator (paying user without entitlement).
func (s *Store) CountStuckActivations(ctx context.Context) (int64, error) {
	const q = `
SELECT count(*) FROM candidate_checkouts
WHERE status = 'paid' AND activated_at IS NULL AND activation_attempts >= $1`
	var n int64
	if err := s.db.QueryRowContext(ctx, q, MaxActivationAttempts).Scan(&n); err != nil {
		return 0, fmt.Errorf("billing: count stuck activations: %w", err)
	}
	return n, nil
}

// ListByCandidate returns recent checkouts for history (newest first).
func (s *Store) ListByCandidate(ctx context.Context, candidateID string, limit int) ([]Checkout, error) {
	if limit <= 0 {
		limit = 50
	}
	const q = `
SELECT prompt_id, candidate_id, plan_id, route, status, subscription_id,
       amount_cents, currency, country, redirect_url, error, created_at, updated_at,
       activated_at, paid_at
FROM candidate_checkouts
WHERE candidate_id = $1
ORDER BY created_at DESC
LIMIT $2
`
	rows, err := s.db.QueryContext(ctx, q, candidateID, limit)
	if err != nil {
		return nil, fmt.Errorf("billing: list by candidate: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]Checkout, 0, 16)
	for rows.Next() {
		c, scanErr := scanRow(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("billing: list by candidate rows: %w", err)
	}
	return out, nil
}

// ListPending returns pending checkouts, oldest first, up to limit. Drives
// the reconciler sweep.
func (s *Store) ListPending(ctx context.Context, limit int) ([]Checkout, error) {
	const q = `
SELECT prompt_id, candidate_id, plan_id, route, status, subscription_id,
       amount_cents, currency, country, redirect_url, error, created_at, updated_at,
       activated_at, paid_at
FROM candidate_checkouts
WHERE status = 'pending'
ORDER BY created_at ASC
LIMIT $1
`
	rows, err := s.db.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("billing: list pending: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]Checkout, 0, 16)
	for rows.Next() {
		c, scanErr := scanRow(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("billing: list pending rows: %w", err)
	}
	return out, nil
}

// ListPaidUnactivated returns paid checkouts whose activation never completed
// and is due for a retry (under the attempt limit, past its backoff, no live
// claim), most overdue first.
func (s *Store) ListPaidUnactivated(ctx context.Context, limit int) ([]Checkout, error) {
	const q = `
SELECT prompt_id, candidate_id, plan_id, route, status, subscription_id,
       amount_cents, currency, country, redirect_url, error, created_at, updated_at,
       activated_at, paid_at
FROM candidate_checkouts
WHERE status = 'paid' AND activated_at IS NULL
  AND activation_attempts < $2
  AND (next_activation_at IS NULL OR next_activation_at <= now())
  AND (activation_claimed_at IS NULL OR activation_claimed_at < now() - make_interval(secs => $3))
ORDER BY COALESCE(next_activation_at, created_at) ASC
LIMIT $1
`
	rows, err := s.db.QueryContext(ctx, q, limit, MaxActivationAttempts, ActivationLease.Seconds())
	if err != nil {
		return nil, fmt.Errorf("billing: list paid unactivated: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]Checkout, 0, 16)
	for rows.Next() {
		c, scanErr := scanRow(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("billing: list paid unactivated rows: %w", err)
	}
	return out, nil
}

type rowScanner interface{ Scan(dest ...any) error }

func (s *Store) scanOne(row rowScanner) (Checkout, error) {
	c, err := scanRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Checkout{}, ErrNotFound
	}
	if err != nil {
		return Checkout{}, fmt.Errorf("billing: scan checkout: %w", err)
	}
	return c, nil
}

func scanRow(row rowScanner) (Checkout, error) {
	var c Checkout
	var status string
	err := row.Scan(
		&c.PromptID, &c.CandidateID, &c.PlanID, &c.Route, &status, &c.SubscriptionID,
		&c.AmountCents, &c.Currency, &c.Country, &c.RedirectURL, &c.Error, &c.CreatedAt, &c.UpdatedAt,
		&c.ActivatedAt, &c.PaidAt,
	)
	c.Status = Status(status)
	return c, err
}
