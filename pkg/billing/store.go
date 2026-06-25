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
       amount_cents, currency, country, redirect_url, error, created_at, updated_at
FROM candidate_checkouts
WHERE prompt_id = $1
`
	return s.scanOne(s.db.QueryRowContext(ctx, q, promptID))
}

// UpdateStatus moves a checkout to a new status, recording the resolved
// subscription id and error. Idempotent and side-effect free beyond the
// row. Returns the post-update row so the caller can drive activation.
func (s *Store) UpdateStatus(ctx context.Context, promptID string, status Status, subscriptionID, errMsg string) (Checkout, error) {
	const q = `
UPDATE candidate_checkouts
SET status = $2,
    subscription_id = CASE WHEN $3 <> '' THEN $3 ELSE subscription_id END,
    error = $4,
    updated_at = now()
WHERE prompt_id = $1
RETURNING prompt_id, candidate_id, plan_id, route, status, subscription_id,
          amount_cents, currency, country, redirect_url, error, created_at, updated_at
`
	return s.scanOne(s.db.QueryRowContext(ctx, q, promptID, string(status), subscriptionID, errMsg))
}

// ListPending returns pending checkouts, oldest first, up to limit. Drives
// the reconciler sweep.
func (s *Store) ListPending(ctx context.Context, limit int) ([]Checkout, error) {
	const q = `
SELECT prompt_id, candidate_id, plan_id, route, status, subscription_id,
       amount_cents, currency, country, redirect_url, error, created_at, updated_at
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
	)
	c.Status = Status(status)
	return c, err
}
