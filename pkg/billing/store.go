package billing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
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
       activated_at
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
    updated_at = now()
WHERE prompt_id = $1
  AND (status IN ('pending', 'redirect') OR (status = 'failed' AND $2 = 'paid'))
  AND $2 IN ('paid', 'failed')
RETURNING prompt_id, candidate_id, plan_id, route, status, subscription_id,
          amount_cents, currency, country, redirect_url, error, created_at, updated_at,
          activated_at
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

// MarkActivated records that the paid checkout's entitlement was granted.
// Idempotent: the first timestamp wins.
func (s *Store) MarkActivated(ctx context.Context, promptID string) error {
	const q = `UPDATE candidate_checkouts SET activated_at = now() WHERE prompt_id = $1 AND activated_at IS NULL`
	if _, err := s.db.ExecContext(ctx, q, promptID); err != nil {
		return fmt.Errorf("billing: mark activated: %w", err)
	}
	return nil
}

// ListByCandidate returns recent checkouts for history (newest first).
func (s *Store) ListByCandidate(ctx context.Context, candidateID string, limit int) ([]Checkout, error) {
	if limit <= 0 {
		limit = 50
	}
	const q = `
SELECT prompt_id, candidate_id, plan_id, route, status, subscription_id,
       amount_cents, currency, country, redirect_url, error, created_at, updated_at,
       activated_at
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
       activated_at
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

// ListPaidUnactivated returns paid checkouts whose activation never completed,
// oldest first, so the reconciler can retry granting the entitlement.
func (s *Store) ListPaidUnactivated(ctx context.Context, limit int) ([]Checkout, error) {
	const q = `
SELECT prompt_id, candidate_id, plan_id, route, status, subscription_id,
       amount_cents, currency, country, redirect_url, error, created_at, updated_at,
       activated_at
FROM candidate_checkouts
WHERE status = 'paid' AND activated_at IS NULL
ORDER BY created_at ASC
LIMIT $1
`
	rows, err := s.db.QueryContext(ctx, q, limit)
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
		&c.ActivatedAt,
	)
	c.Status = Status(status)
	return c, err
}
