// pkg/frontier/admin.go — operator query surface on url_frontier.
//
// Kept separate from the hot-path Postgres implementation so the
// admin reads + state mutations (List / Get / Requeue / Delete)
// are easy to audit and don't accidentally interleave with the
// Dequeue locking discipline.
package frontier

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// AdminRow is the operator-facing snapshot of one url_frontier
// row. Returned by AdminRepository.List + .Get. Includes the
// audit fields (LastError, ClaimedBy, CompletedAt) that the hot
// path returns separately.
type AdminRow struct {
	URLID         string     `json:"url_id"`
	CanonicalURL  string     `json:"canonical_url"`
	Host          string     `json:"host"`
	SourceID      string     `json:"source_id"`
	Priority      float64    `json:"priority"`
	State         string     `json:"state"`
	Attempts      int        `json:"attempts"`
	LastError     string     `json:"last_error,omitempty"`
	EnqueuedAt    time.Time  `json:"enqueued_at"`
	ClaimedAt     *time.Time `json:"claimed_at,omitempty"`
	ClaimedBy     string     `json:"claimed_by,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	Metadata      []byte     `json:"metadata,omitempty"`
}

// AdminFilter is the filter bag for AdminRepository.List.
// Empty fields are treated as "no filter for this dimension".
type AdminFilter struct {
	State    State
	Host     string
	SourceID string
	Limit    int
	Offset   int
}

// AdminRepository exposes the operator query surface.
type AdminRepository struct {
	db PoolFn
}

// NewAdminRepository wires the repo.
func NewAdminRepository(db PoolFn) *AdminRepository {
	return &AdminRepository{db: db}
}

// List returns a page of rows matching filter plus the total
// count without limit. Ordering is (priority DESC, enqueued_at
// ASC) — the same ordering the Dequeue path uses, so the admin
// view always shows the next batch a worker would claim.
func (r *AdminRepository) List(ctx context.Context, f AdminFilter) ([]AdminRow, int64, error) {
	q := r.db(ctx, true).Table("url_frontier")
	if f.State != "" {
		q = q.Where("state = ?", string(f.State))
	}
	if f.Host != "" {
		q = q.Where("host = ?", f.Host)
	}
	if f.SourceID != "" {
		q = q.Where("source_id = ?", f.SourceID)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("frontier list count: %w", err)
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	var rows []row
	err := q.Order("priority DESC, enqueued_at ASC").
		Limit(limit).
		Offset(f.Offset).
		Find(&rows).Error
	if err != nil {
		return nil, 0, fmt.Errorf("frontier list find: %w", err)
	}

	out := make([]AdminRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, toAdminRow(r))
	}
	return out, total, nil
}

// Get returns one row by url_id or (nil, nil) when absent.
func (r *AdminRepository) Get(ctx context.Context, urlID string) (*AdminRow, error) {
	var r0 row
	err := r.db(ctx, true).Where("url_id = ?", urlID).First(&r0).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("frontier get: %w", err)
	}
	ar := toAdminRow(r0)
	return &ar, nil
}

// Requeue resets a row's state to 'pending' and clears
// next_attempt_at so the next Dequeue tick picks it up. Returns
// the row after the update, or (nil, nil) when not found. The
// attempts counter is bumped only when the row was 'failed'
// (operator escape hatch — we want to track that an operator
// intervened after the retry budget ran out).
func (r *AdminRepository) Requeue(ctx context.Context, urlID string) (*AdminRow, error) {
	var prior row
	err := r.db(ctx, false).Where("url_id = ?", urlID).First(&prior).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("frontier requeue load: %w", err)
	}
	updates := map[string]any{
		"state":           string(StatePending),
		"next_attempt_at": nil,
		"last_error":      "",
	}
	if prior.State == string(StateFailed) {
		updates["attempts"] = gorm.Expr("attempts + 1")
	}
	if err := r.db(ctx, false).
		Model(&row{}).
		Where("url_id = ?", urlID).
		Updates(updates).Error; err != nil {
		return nil, fmt.Errorf("frontier requeue update: %w", err)
	}
	return r.Get(ctx, urlID)
}

// Delete hard-deletes the row. Operator override for stuck rows
// that should never be retried — distinct from Requeue which
// keeps the row in the queue. Idempotent.
func (r *AdminRepository) Delete(ctx context.Context, urlID string) error {
	return r.db(ctx, false).
		Where("url_id = ?", urlID).
		Delete(&row{}).Error
}

func toAdminRow(r row) AdminRow {
	return AdminRow{
		URLID:         r.URLID,
		CanonicalURL:  r.CanonicalURL,
		Host:          r.Host,
		SourceID:      r.SourceID,
		Priority:      r.Priority,
		State:         r.State,
		Attempts:      r.Attempts,
		LastError:     r.LastError,
		EnqueuedAt:    r.EnqueuedAt,
		ClaimedAt:     r.ClaimedAt,
		ClaimedBy:     r.ClaimedBy,
		CompletedAt:   r.CompletedAt,
		NextAttemptAt: r.NextAttemptAt,
		Metadata:      r.Metadata,
	}
}
