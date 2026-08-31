package matching

import (
	"context"
	"database/sql"
	"fmt"
)

// PGDailyCapQuery counts today's generated matches from candidate_match_events.
type PGDailyCapQuery struct {
	db *sql.DB
}

func NewPGDailyCapQuery(db *sql.DB) *PGDailyCapQuery {
	return &PGDailyCapQuery{db: db}
}

// TodayCount returns the number of generated matches written today for
// the candidate, counted directly from the append-only events table.
// "Today" is the current UTC day, matching the alignment of the daily
// buckets this query historically read.
func (q *PGDailyCapQuery) TodayCount(ctx context.Context, candidateID string) (int, error) {
	const sql_ = `
SELECT count(*)
  FROM candidate_match_events
 WHERE candidate_id = $1
   AND kind = 'generated'
   AND occurred_at >= date_trunc('day', now() AT TIME ZONE 'utc') AT TIME ZONE 'utc'
`
	var n int
	if err := q.db.QueryRowContext(ctx, sql_, candidateID).Scan(&n); err != nil {
		return 0, fmt.Errorf("matching: daily cap query: %w", err)
	}
	return n, nil
}
