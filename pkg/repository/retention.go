package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/xid"
	"gorm.io/gorm"
)

// RetentionPolicy says how long crawl-plane rows are kept. The defaults
// reproduce the policy the pipeline was designed with (the TimescaleDB jobs
// in 20260706_0140, which never ran on Neon). Zero disables a rule.
type RetentionPolicy struct {
	ProcessedQueue time.Duration // job_ingest_queue processed rows
	TerminalQueue  time.Duration // job_ingest_queue dead / rejected rows
	Events         time.Duration // job_ingest_events (>= 90d: ingest_events_guard refuses younger)
	CrawlJobs      time.Duration // crawl_jobs succeeded / failed
	CrawlRuns      time.Duration // crawl_runs completed / failed
	Frontier       time.Duration // url_frontier done / failed, by completion (a URL re-discovered later is re-fetched)
	DoneCommands   time.Duration // catalog_commands done / superseded (dead ones are kept: they drive an alert)

	BatchSize  int // rows per DELETE statement
	MaxBatches int // per rule per sweep, so one sweep stays bounded
}

// DefaultRetentionPolicy is the production policy.
func DefaultRetentionPolicy() RetentionPolicy {
	const day = 24 * time.Hour
	return RetentionPolicy{
		ProcessedQueue: 30 * day,
		TerminalQueue:  90 * day,
		Events:         90 * day,
		CrawlJobs:      90 * day,
		CrawlRuns:      90 * day,
		Frontier:       90 * day,
		DoneCommands:   30 * day,
		BatchSize:      5000,
		MaxBatches:     200,
	}
}

// RetentionResult reports rows deleted per table in one sweep.
type RetentionResult map[string]int64

// minEventRetention mirrors the age floor enforced by ingest_events_guard()
// (migration 20261122_0024).
const minEventRetention = 90 * 24 * time.Hour

// xidBoundSlack widens the xid primary-key bound past the cutoff so app/DB
// clock skew can never hide an expired row; the exact cutoff is applied in
// SQL with the database clock.
const xidBoundSlack = 24 * time.Hour

type retentionRule struct {
	table string
	// idCol is the xid primary-key column. xids embed their creation time and
	// sort by it, so "id < xid(cutoff)" bounds every scan to old rows using
	// the existing primary-key index (no extra index, no full scan). Empty
	// for small tables.
	idCol string
	// where is the exact predicate; ? is the retention window in seconds,
	// evaluated against the database clock.
	where   string
	keep    time.Duration
	guarded bool // needs the retention session flag (append-only trigger)
}

// SweepCrawlRetention deletes expired crawl-plane rows in bounded batches.
// Every rule runs even if an earlier one fails; the errors are joined.
// Deletes are idempotent, so concurrent replicas only duplicate work.
func SweepCrawlRetention(ctx context.Context, db func(context.Context, bool) *gorm.DB, p RetentionPolicy) (RetentionResult, error) {
	if p.BatchSize <= 0 {
		p.BatchSize = 5000
	}
	if p.MaxBatches <= 0 {
		p.MaxBatches = 200
	}
	if p.Events > 0 && p.Events < minEventRetention {
		p.Events = minEventRetention
	}
	rules := []retentionRule{
		{table: "job_ingest_queue", idCol: "id", where: "status = 'processed' AND processed_at < now() - make_interval(secs => ?)", keep: p.ProcessedQueue},
		{table: "job_ingest_queue", idCol: "id", where: "status IN ('dead','rejected') AND updated_at < now() - make_interval(secs => ?)", keep: p.TerminalQueue},
		{table: "job_ingest_events", idCol: "event_id", where: "occurred_at < now() - make_interval(secs => ?)", keep: p.Events, guarded: true},
		{table: "crawl_jobs", idCol: "id", where: "status IN ('succeeded','failed') AND created_at < now() - make_interval(secs => ?)", keep: p.CrawlJobs},
		{table: "crawl_runs", where: "status IN ('completed','failed') AND completed_at < now() - make_interval(secs => ?)", keep: p.CrawlRuns},
		{table: "url_frontier", idCol: "url_id", where: "state IN ('done','failed') AND COALESCE(completed_at, enqueued_at) < now() - make_interval(secs => ?)", keep: p.Frontier},
		{table: "catalog_commands", idCol: "id", where: "status IN ('done','superseded') AND updated_at < now() - make_interval(secs => ?)", keep: p.DoneCommands},
	}
	res := RetentionResult{}
	var errs []error
	for _, r := range rules {
		if r.keep <= 0 {
			continue
		}
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
		n, err := deleteExpired(ctx, db, r, p.BatchSize, p.MaxBatches)
		res[r.table] += n
		if err != nil {
			errs = append(errs, fmt.Errorf("retention %s: %w", r.table, err))
		}
	}
	return res, errors.Join(errs...)
}

// xidUpperBound is the smallest xid created at t (zero machine/pid/counter).
func xidUpperBound(t time.Time) string {
	var b [12]byte
	ts := uint32(t.Unix())
	b[0], b[1], b[2], b[3] = byte(ts>>24), byte(ts>>16), byte(ts>>8), byte(ts)
	id, _ := xid.FromBytes(b[:])
	return id.String()
}

func deleteExpired(ctx context.Context, db func(context.Context, bool) *gorm.DB, r retentionRule, batch, maxBatches int) (int64, error) {
	if r.idCol == "" {
		return deleteExpiredUnbounded(ctx, db, r, batch, maxBatches)
	}
	// Keyset over the xid primary key: each batch takes the next expired
	// rows in id order after the last one deleted, so old rows that are not
	// deletable (parked, pending, dead) are passed over once, not re-read by
	// every batch. upper bounds the scan to rows created before the cutoff.
	upper := xidUpperBound(time.Now().Add(-r.keep + xidBoundSlack))
	stmt := fmt.Sprintf(`DELETE FROM %[1]s WHERE ctid IN (
		SELECT ctid FROM %[1]s
		WHERE %[2]s > ? AND %[2]s < ? AND %[3]s
		ORDER BY %[2]s LIMIT ?)
		RETURNING %[2]s`, r.table, r.idCol, r.where)

	var total int64
	after := ""
	for i := 0; i < maxBatches; i++ {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		var deleted []string
		err := db(ctx, false).WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if r.guarded {
				if err := tx.Exec(`SET LOCAL stawi.retention_sweep = 'on'`).Error; err != nil {
					return err
				}
			}
			return tx.Raw(stmt, after, upper, r.keep.Seconds(), batch).Scan(&deleted).Error
		})
		if err != nil {
			return total, err
		}
		total += int64(len(deleted))
		if len(deleted) < batch {
			return total, nil
		}
		for _, id := range deleted {
			if id > after {
				after = id
			}
		}
	}
	return total, nil
}

// deleteExpiredUnbounded is for small tables without an xid key.
func deleteExpiredUnbounded(ctx context.Context, db func(context.Context, bool) *gorm.DB, r retentionRule, batch, maxBatches int) (int64, error) {
	stmt := fmt.Sprintf(`DELETE FROM %[1]s WHERE ctid IN (SELECT ctid FROM %[1]s WHERE %[2]s LIMIT ?)`, r.table, r.where)
	var total int64
	for i := 0; i < maxBatches; i++ {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		res := db(ctx, false).WithContext(ctx).Exec(stmt, r.keep.Seconds(), batch)
		if res.Error != nil {
			return total, res.Error
		}
		total += res.RowsAffected
		if res.RowsAffected < int64(batch) {
			return total, nil
		}
	}
	return total, nil
}
