package matching

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// InvokeCounter counts user-facing MatchInvoke runs for a candidate on a UTC day.
type InvokeCounter interface {
	CountUserInvokesToday(ctx context.Context, candidateID string, now time.Time) (int, error)
}

// PGInvokeCounter counts match_run_events for user_refresh|onboard_seed since UTC midnight.
type PGInvokeCounter struct {
	DB *sql.DB
}

// CountUserInvokesToday returns how many budget-consuming invokes the candidate
// has already started today (UTC). Counts match_run_events where triggered_by
// is user_refresh or onboard_seed and started_at >= UTC midnight of now.
func (c *PGInvokeCounter) CountUserInvokesToday(ctx context.Context, candidateID string, now time.Time) (int, error) {
	u := now.UTC()
	dayStart := time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
	const q = `
SELECT count(*) FROM match_run_events
 WHERE candidate_id = $1
   AND triggered_by IN ('user_refresh', 'onboard_seed')
   AND started_at >= $2`
	var n int
	if err := c.DB.QueryRowContext(ctx, q, candidateID, dayStart).Scan(&n); err != nil {
		return 0, fmt.Errorf("matching: count invokes: %w", err)
	}
	return n, nil
}
