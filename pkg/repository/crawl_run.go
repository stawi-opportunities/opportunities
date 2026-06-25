// Package repository: CrawlRunRepository persists crawl_runs — the durable
// state machine that makes crawling resumable and bounded. One active run per
// source is enforced by the partial unique index idx_crawl_runs_active
// (apps/crawler/migrations/0001/20260612_0134_crawl_runs_active_uniq.sql); a
// per-source lease (owner + lease_expires_at) serialises the consumers driving
// a run's slices, and the watchdog reclaims runs whose lease lapsed.
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// CrawlRunRepository is the data-access seam for crawl_runs. All mutations read
// and write the primary (DB(ctx,false)) — these are read-modify-write claims
// where replica lag would miss a just-written row.
type CrawlRunRepository struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

// NewCrawlRunRepository wires the repo with the standard pool factory shape.
func NewCrawlRunRepository(db func(ctx context.Context, readOnly bool) *gorm.DB) *CrawlRunRepository {
	return &CrawlRunRepository{db: db}
}

// GetByID returns the run or (nil, nil) when absent.
func (r *CrawlRunRepository) GetByID(ctx context.Context, id string) (*domain.CrawlRun, error) {
	var run domain.CrawlRun
	err := r.db(ctx, false).Where("id = ?", id).First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

// GetActiveBySource returns the running/paused run for a source, or nil.
func (r *CrawlRunRepository) GetActiveBySource(ctx context.Context, sourceID string) (*domain.CrawlRun, error) {
	var run domain.CrawlRun
	err := r.db(ctx, false).
		Where("source_id = ? AND status IN ?", sourceID, domain.ActiveCrawlRunStatuses).
		First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

// StartRun begins a fresh full-pass for a source on a scheduled tick. It returns
// (run, started=true) when it created and now owns a fresh run, or
// (existingRun, started=false) when an active run already exists — the caller
// must then drop the scheduled delivery (single-flight: one pass per source).
//
// The race is resolved by the DB: two concurrent ticks both attempt the insert;
// the partial unique index lets exactly one win, the loser falls back to the
// existing active row.
func (r *CrawlRunRepository) StartRun(
	ctx context.Context, sourceID string, scheduledAt time.Time, owner string, leaseTTL time.Duration,
) (*domain.CrawlRun, bool, error) {
	now := time.Now().UTC()
	if scheduledAt.IsZero() {
		scheduledAt = now
	}
	lease := now.Add(leaseTTL)
	run := &domain.CrawlRun{
		SourceID:       sourceID,
		Status:         domain.CrawlRunRunning,
		Owner:          owner,
		LeaseExpiresAt: &lease,
		ScheduledAt:    scheduledAt,
		StartedAt:      now,
		LastProgressAt: now,
	}
	// ON CONFLICT DO NOTHING against the partial unique index: a no-op insert
	// when an active run already exists. RowsAffected==0 → an active run is
	// in flight; load and return it.
	res := r.db(ctx, false).Clauses(clause.OnConflict{DoNothing: true}).Create(run)
	if res.Error != nil {
		return nil, false, res.Error
	}
	if res.RowsAffected == 1 {
		return run, true, nil
	}
	existing, err := r.GetActiveBySource(ctx, sourceID)
	if err != nil {
		return nil, false, err
	}
	return existing, false, nil
}

// Claim takes the lease on a run for a continuation slice. It succeeds only when
// the run is active and the lease is free (unowned or expired) — so a second
// consumer handling a redelivered continuation loses and drops. Returns the
// fresh run state and whether the claim was granted.
func (r *CrawlRunRepository) Claim(
	ctx context.Context, id, owner string, leaseTTL time.Duration,
) (*domain.CrawlRun, bool, error) {
	now := time.Now().UTC()
	res := r.db(ctx, false).
		Model(&domain.CrawlRun{}).
		Where("id = ? AND status IN ? AND (owner = '' OR owner IS NULL OR owner = ? OR lease_expires_at IS NULL OR lease_expires_at < ?)",
			id, domain.ActiveCrawlRunStatuses, owner, now).
		Updates(map[string]any{
			"owner":            owner,
			"lease_expires_at": now.Add(leaseTTL),
			"status":           domain.CrawlRunRunning,
			"updated_at":       now,
		})
	if res.Error != nil {
		return nil, false, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, false, nil
	}
	run, err := r.GetByID(ctx, id)
	return run, run != nil, err
}

// Progress persists per-page state and renews the lease so an actively-running
// slice is never reclaimed by the watchdog. Counters are accumulated in the
// handler and flushed at slice end (Yield/Complete), not per page.
func (r *CrawlRunRepository) Progress(
	ctx context.Context, id string, cursor json.RawMessage, pageIdx int, lastURL string, leaseTTL time.Duration,
) error {
	now := time.Now().UTC()
	updates := map[string]any{
		"cursor":           datatypesOrNil(cursor),
		"page_idx":         pageIdx,
		"last_progress_at": now,
		"lease_expires_at": now.Add(leaseTTL),
		"updated_at":       now,
	}
	if lastURL != "" {
		updates["last_url"] = lastURL
	}
	return r.db(ctx, false).Model(&domain.CrawlRun{}).Where("id = ?", id).Updates(updates).Error
}

// Yield ends a slice with pages still remaining: persist the cursor, count the
// slice, accumulate the slice's job tallies, and release the lease while leaving
// the run running. lease_expires_at is stamped now+leaseTTL as a debounce window
// so the watchdog won't re-emit before the in-flight continuation is consumed.
func (r *CrawlRunRepository) Yield(
	ctx context.Context, id string, cursor json.RawMessage, pageIdx int, lastURL string,
	leaseTTL time.Duration, dFound, dEmitted, dRejected int,
) error {
	now := time.Now().UTC()
	updates := map[string]any{
		"cursor":           datatypesOrNil(cursor),
		"page_idx":         pageIdx,
		"status":           domain.CrawlRunRunning,
		"owner":            "",
		"lease_expires_at": now.Add(leaseTTL),
		"slice_count":      gorm.Expr("slice_count + 1"),
		"attempt":          gorm.Expr("attempt + 1"),
		"jobs_found":       gorm.Expr("jobs_found + ?", dFound),
		"jobs_emitted":     gorm.Expr("jobs_emitted + ?", dEmitted),
		"jobs_rejected":    gorm.Expr("jobs_rejected + ?", dRejected),
		"last_progress_at": now,
		"updated_at":       now,
	}
	if lastURL != "" {
		updates["last_url"] = lastURL
	}
	return r.db(ctx, false).Model(&domain.CrawlRun{}).Where("id = ?", id).Updates(updates).Error
}

// Complete marks a run finished (the iterator reached the end of the board),
// releasing the lease so the next scheduled tick can begin a fresh pass.
func (r *CrawlRunRepository) Complete(
	ctx context.Context, id string, cursor json.RawMessage, pageIdx int, lastURL string,
	dFound, dEmitted, dRejected int,
) error {
	now := time.Now().UTC()
	updates := map[string]any{
		"cursor":           datatypesOrNil(cursor),
		"page_idx":         pageIdx,
		"status":           domain.CrawlRunCompleted,
		"owner":            "",
		"lease_expires_at": nil,
		"completed_at":     now,
		"slice_count":      gorm.Expr("slice_count + 1"),
		"jobs_found":       gorm.Expr("jobs_found + ?", dFound),
		"jobs_emitted":     gorm.Expr("jobs_emitted + ?", dEmitted),
		"jobs_rejected":    gorm.Expr("jobs_rejected + ?", dRejected),
		"last_progress_at": now,
		"updated_at":       now,
	}
	if lastURL != "" {
		updates["last_url"] = lastURL
	}
	return r.db(ctx, false).Model(&domain.CrawlRun{}).Where("id = ?", id).Updates(updates).Error
}

// Fail marks a run terminally failed and releases the lease (leaving the source
// free to start a fresh pass on its next scheduled tick).
func (r *CrawlRunRepository) Fail(ctx context.Context, id, code, message string, dFound, dEmitted, dRejected int) error {
	now := time.Now().UTC()
	return r.db(ctx, false).Model(&domain.CrawlRun{}).Where("id = ?", id).Updates(map[string]any{
		"status":           domain.CrawlRunFailed,
		"owner":            "",
		"lease_expires_at": nil,
		"error_code":       code,
		"error_message":    message,
		"slice_count":      gorm.Expr("slice_count + 1"),
		"jobs_found":       gorm.Expr("jobs_found + ?", dFound),
		"jobs_emitted":     gorm.Expr("jobs_emitted + ?", dEmitted),
		"jobs_rejected":    gorm.Expr("jobs_rejected + ?", dRejected),
		"last_progress_at": now,
		"updated_at":       now,
	}).Error
}

// ListRuns returns recent runs, newest-updated first, optionally filtered to a
// source. Powers the /admin/crawl-runs operator surface.
func (r *CrawlRunRepository) ListRuns(ctx context.Context, sourceID string, limit int) ([]*domain.CrawlRun, error) {
	if limit <= 0 {
		limit = 100
	}
	q := r.db(ctx, true).Order("updated_at DESC").Limit(limit)
	if sourceID != "" {
		q = q.Where("source_id = ?", sourceID)
	}
	var runs []*domain.CrawlRun
	if err := q.Find(&runs).Error; err != nil {
		return nil, err
	}
	return runs, nil
}

// TouchLease pushes a run's lease forward without taking ownership — the
// watchdog calls it when re-driving a lapsed run so the same run isn't
// re-emitted on the next tick before its fresh continuation is consumed. Owner
// stays cleared so the handler's claim (any owner) still succeeds.
func (r *CrawlRunRepository) TouchLease(ctx context.Context, id string, leaseTTL time.Duration) error {
	now := time.Now().UTC()
	return r.db(ctx, false).
		Model(&domain.CrawlRun{}).
		Where("id = ? AND status IN ?", id, domain.ActiveCrawlRunStatuses).
		Updates(map[string]any{"lease_expires_at": now.Add(leaseTTL), "updated_at": now}).Error
}

// FindResumable returns active runs whose lease has lapsed (a crashed owner, a
// lost continuation, or a backpressure-deferred yield past its debounce). The
// watchdog re-drives these. Actively-leased runs (lease in the future) are
// skipped. Ordered oldest-progress-first so the most starved runs go first.
func (r *CrawlRunRepository) FindResumable(ctx context.Context, limit int) ([]*domain.CrawlRun, error) {
	now := time.Now().UTC()
	var runs []*domain.CrawlRun
	err := r.db(ctx, true).
		Where("status IN ? AND (lease_expires_at IS NULL OR lease_expires_at < ?)",
			domain.ActiveCrawlRunStatuses, now).
		Order("last_progress_at ASC").
		Limit(limit).
		Find(&runs).Error
	if err != nil {
		return nil, err
	}
	return runs, nil
}

// datatypesOrNil keeps an empty cursor out of the column as SQL NULL rather than
// writing an invalid empty jsonb value.
func datatypesOrNil(cursor json.RawMessage) any {
	if len(cursor) == 0 {
		return nil
	}
	return cursor
}
