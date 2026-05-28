// Package savedjobs is the read+write surface for candidate-starred
// opportunities. Backed by the candidate_saved_jobs table (see
// db/migrations/0018_candidate_saved_jobs.sql). Sibling to
// pkg/matching/store.go; same raw-sql style for the same reason —
// the queries are aggregate-shaped and don't fit BaseRepository.
package savedjobs

import (
	"context"
	"database/sql"
	"fmt"
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// Star marks (candidate, opportunity) as saved. Idempotent: starring
// the same pair twice returns nil with no error.
func (s *Store) Star(ctx context.Context, candidateID, opportunityID string) error {
	const q = `
INSERT INTO candidate_saved_jobs (candidate_id, opportunity_id)
VALUES ($1, $2)
ON CONFLICT (candidate_id, opportunity_id) DO NOTHING
`
	if _, err := s.db.ExecContext(ctx, q, candidateID, opportunityID); err != nil {
		return fmt.Errorf("savedjobs: star: %w", err)
	}
	return nil
}

// Unstar removes the (candidate, opportunity) pair. Idempotent: a
// pair that was never starred returns nil.
func (s *Store) Unstar(ctx context.Context, candidateID, opportunityID string) error {
	const q = `DELETE FROM candidate_saved_jobs WHERE candidate_id = $1 AND opportunity_id = $2`
	if _, err := s.db.ExecContext(ctx, q, candidateID, opportunityID); err != nil {
		return fmt.Errorf("savedjobs: unstar: %w", err)
	}
	return nil
}

// ListByCandidate returns the opportunity IDs the candidate has
// starred, most-recent first. Caller joins these against the
// opportunities table to materialise the snapshot — this package
// only knows the star list.
func (s *Store) ListByCandidate(ctx context.Context, candidateID string) ([]string, error) {
	const q = `
SELECT opportunity_id
FROM candidate_saved_jobs
WHERE candidate_id = $1
ORDER BY created_at DESC, opportunity_id
`
	rows, err := s.db.QueryContext(ctx, q, candidateID)
	if err != nil {
		return nil, fmt.Errorf("savedjobs: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]string, 0, 16)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("savedjobs: scan: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("savedjobs: rows: %w", err)
	}
	return out, nil
}
