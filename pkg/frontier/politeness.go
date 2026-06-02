// pkg/frontier/politeness.go — operator-facing helpers on
// host_state. The Frontier interface already enforces the
// per-host window + concurrency cap at Dequeue time; this file
// adds the surface the admin endpoints + backfill paths need:
// list hosts, override window, count rolling buckets.
package frontier

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// HostState is the operator-facing snapshot of one host_state
// row. Returned by HostStateRepository.List + .Get for the
// /admin/frontier/hosts surface.
type HostState struct {
	Host            string     `json:"host"`
	WindowMinutes   int        `json:"window_minutes"`
	LastRequestAt   *time.Time `json:"last_request_at,omitempty"`
	NextEligibleAt  *time.Time `json:"next_eligible_at,omitempty"`
	OkCount24H      int        `json:"ok_count_24h"`
	ErrCount24H     int        `json:"err_count_24h"`
	ConcurrencyMax  int        `json:"concurrency_max"`
	ConcurrencyNow  int        `json:"concurrency_now"`
}

// HostStateRepository exposes the operator + backfill surface
// against host_state. Construction mirrors the rest of the
// repositories: a pool factory.
type HostStateRepository struct {
	db PoolFn
}

// NewHostStateRepository wires the repo.
func NewHostStateRepository(db PoolFn) *HostStateRepository {
	return &HostStateRepository{db: db}
}

// Upsert ensures a row exists for host without touching the
// rolling counters / concurrency_now (it is a Dequeue-controlled
// column). Used both by frontier.Enqueue's ensureHost path and
// by the backfill from sources.base_url.
func (r *HostStateRepository) Upsert(ctx context.Context, host string, windowMinutes, concurrencyMax int) error {
	if host == "" {
		return fmt.Errorf("host_state upsert: empty host")
	}
	if windowMinutes <= 0 {
		windowMinutes = 1
	}
	if concurrencyMax <= 0 {
		concurrencyMax = 1
	}
	hs := hostStateRow{
		Host:           host,
		WindowMinutes:  windowMinutes,
		ConcurrencyMax: concurrencyMax,
	}
	// On conflict do nothing — the politeness gate is
	// operator-owned; an upsert shouldn't trample tuned values.
	return r.db(ctx, false).
		Session(&gorm.Session{PrepareStmt: false}).
		Exec(`
			INSERT INTO host_state (host, window_minutes, concurrency_max)
			VALUES (?, ?, ?)
			ON CONFLICT (host) DO NOTHING`,
			hs.Host, hs.WindowMinutes, hs.ConcurrencyMax,
		).Error
}

// Get returns the politeness snapshot for one host or (nil, nil)
// when the host is unknown.
func (r *HostStateRepository) Get(ctx context.Context, host string) (*HostState, error) {
	var hs hostStateRow
	err := r.db(ctx, true).
		Where("host = ?", host).
		First(&hs).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("host_state get: %w", err)
	}
	return toHostState(hs), nil
}

// List returns every host_state row ordered by next_eligible_at
// ASC NULLS FIRST so the operator UI surfaces hosts due now or
// already past due at the top.
func (r *HostStateRepository) List(ctx context.Context, limit int) ([]HostState, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	var rows []hostStateRow
	err := r.db(ctx, true).
		Order("next_eligible_at ASC NULLS FIRST").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("host_state list: %w", err)
	}
	out := make([]HostState, 0, len(rows))
	for _, r := range rows {
		out = append(out, *toHostState(r))
	}
	return out, nil
}

// SetWindow updates the per-host politeness window. Operator-only
// — used to tune hosts that publicly advertise higher (or lower)
// tolerances.
func (r *HostStateRepository) SetWindow(ctx context.Context, host string, windowMinutes int) error {
	if windowMinutes < 0 {
		return fmt.Errorf("host_state set window: negative minutes")
	}
	return r.db(ctx, false).
		Model(&hostStateRow{}).
		Where("host = ?", host).
		Update("window_minutes", windowMinutes).Error
}

// SetConcurrency raises (or lowers) the per-host in-flight cap.
func (r *HostStateRepository) SetConcurrency(ctx context.Context, host string, concurrencyMax int) error {
	if concurrencyMax <= 0 {
		return fmt.Errorf("host_state set concurrency: must be positive")
	}
	return r.db(ctx, false).
		Model(&hostStateRow{}).
		Where("host = ?", host).
		Update("concurrency_max", concurrencyMax).Error
}

func toHostState(r hostStateRow) *HostState {
	return &HostState{
		Host:            r.Host,
		WindowMinutes:   r.WindowMinutes,
		LastRequestAt:   r.LastRequestAt,
		NextEligibleAt:  r.NextEligibleAt,
		OkCount24H:      r.OkCount24H,
		ErrCount24H:     r.ErrCount24H,
		ConcurrencyMax:  r.ConcurrencyMax,
		ConcurrencyNow:  r.ConcurrencyNow,
	}
}
