package matching

import (
	"context"
	"database/sql"
	"fmt"
)

// PGDailyCapQuery reads candidate_match_events_daily for today's count.
type PGDailyCapQuery struct {
	db *sql.DB
}

func NewPGDailyCapQuery(db *sql.DB) *PGDailyCapQuery {
	return &PGDailyCapQuery{db: db}
}

// TodayCount returns the number of generated matches written today for
// the candidate. Uses the continuous aggregate when available; falls
// back to the raw hypertable when the CAGG hasn't refreshed yet.
func (q *PGDailyCapQuery) TodayCount(ctx context.Context, candidateID string) (int, error) {
	// Continuous aggregates lag by up to the refresh interval (5 min).
	// Read the CAGG and UNION the recent-tail from the raw hypertable
	// so the count is always current.
	const sql_ = `
SELECT
    COALESCE(SUM(matches_generated), 0)
  + (
        SELECT count(*)
          FROM candidate_match_events
         WHERE candidate_id = $1
           AND kind = 'generated'
           AND occurred_at > (now() - INTERVAL '10 minutes')
    ) AS used
  FROM candidate_match_events_daily
 WHERE candidate_id = $1
   AND day = time_bucket(INTERVAL '1 day', now())
`
	var n int
	if err := q.db.QueryRowContext(ctx, sql_, candidateID).Scan(&n); err != nil {
		return 0, fmt.Errorf("matching: daily cap query: %w", err)
	}
	return n, nil
}
