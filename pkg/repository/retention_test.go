//go:build integration

package repository_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rs/xid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
)

func countWhere(t *testing.T, g *gorm.DB, table, where string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, g.Raw(fmt.Sprintf(`SELECT count(*) FROM %s WHERE %s`, table, where)).Scan(&n).Error)
	return n
}

// ids maps readable test names to real xids whose embedded time is the row's
// age, as in production (rows are inserted with a fresh xid).
type ids map[string]string

func (m ids) at(name string, ago time.Duration) string {
	id := xid.NewWithTime(time.Now().Add(-ago)).String()
	m[name] = id
	return id
}

func (m ids) in(names ...string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = "'" + m[n] + "'"
	}
	return "(" + strings.Join(q, ",") + ")"
}

const day = 24 * time.Hour

// seedRetention creates, per table, rows that must go and rows that must stay.
func seedRetention(t *testing.T, g *gorm.DB) ids {
	t.Helper()
	m := ids{}
	exec := func(q string, args ...any) { t.Helper(); require.NoError(t, g.Exec(q, args...).Error) }
	queue := func(name, status, col string, age time.Duration) {
		id := m.at(name, age)
		exec(`INSERT INTO job_ingest_queue (id, variant_id, source_id, idempotency_key, payload, status, created_at)
			VALUES (?, ?, 's', ?, '{}', ?, now() - make_interval(secs => ?))`, id, id, id, status, age.Seconds())
		exec(fmt.Sprintf(`UPDATE job_ingest_queue SET %s = now() - make_interval(secs => ?) WHERE id = ?`, col), age.Seconds(), id)
	}
	queue("q-old-processed", "processed", "processed_at", 31*day)
	queue("q-new-processed", "processed", "processed_at", 1*day)
	queue("q-old-dead", "dead", "updated_at", 91*day)
	queue("q-new-dead", "dead", "updated_at", 10*day)
	queue("q-old-pending", "pending", "updated_at", 200*day) // live work: never deleted

	event := func(name string, age time.Duration) {
		exec(`INSERT INTO job_ingest_events (event_id, occurred_at, ingest_id, variant_id, source_id, event_type, attempt, details)
			VALUES (?, now() - make_interval(secs => ?), 'i', 'v', 's', 'enqueued', 0, '{}')`, m.at(name, age), age.Seconds())
	}
	event("e-old", 91*day)
	event("e-new", 89*day)

	job := func(name string, status domain.CrawlJobStatus, age time.Duration) {
		id := m.at(name, age)
		require.NoError(t, g.Create(&domain.CrawlJob{ID: id, ScheduledAt: time.Now(), SourceID: "s", Status: status, IdempotencyKey: id}).Error)
		exec(`UPDATE crawl_jobs SET created_at = now() - make_interval(secs => ?) WHERE id = ?`, age.Seconds(), id)
	}
	job("j-old-ok", domain.CrawlSucceeded, 91*day)
	job("j-old-running", domain.CrawlRunning, 91*day)
	job("j-new-ok", domain.CrawlSucceeded, 1*day)

	run := func(name, source string, status domain.CrawlRunStatus, completedAgo time.Duration) {
		id := m.at(name, completedAgo)
		exec(`INSERT INTO crawl_runs (id, source_id, status) VALUES (?, ?, ?)`, id, source, string(status))
		if completedAgo > 0 {
			exec(`UPDATE crawl_runs SET completed_at = now() - make_interval(secs => ?) WHERE id = ?`, completedAgo.Seconds(), id)
		}
	}
	run("r-old-done", "s1", domain.CrawlRunCompleted, 91*day)
	run("r-new-done", "s2", domain.CrawlRunCompleted, 1*day)
	run("r-paused", "s3", domain.CrawlRunPaused, 0)

	url := func(name, state string, enqueuedAgo, completedAgo time.Duration) {
		id := m.at(name, enqueuedAgo)
		exec(`INSERT INTO url_frontier (url_id, canonical_url, canonical_url_hash, host, source_id, state, enqueued_at)
			VALUES (?, ?, ?, 'h.example.com', 's', ?, now() - make_interval(secs => ?))`, id, "https://h.example.com/"+id, id, state, enqueuedAgo.Seconds())
		if completedAgo > 0 {
			exec(`UPDATE url_frontier SET completed_at = now() - make_interval(secs => ?) WHERE url_id = ?`, completedAgo.Seconds(), id)
		}
	}
	url("u-old-done", "done", 120*day, 91*day)
	url("u-old-enqueued-recently-done", "done", 120*day, 5*day) // aged by completion, not enqueue
	url("u-old-pending", "pending", 200*day, 0)
	url("u-old-parked", "parked", 200*day, 0)
	url("u-new-done", "done", 1*day, 1*day)

	cmd := func(name, status string, age time.Duration) {
		exec(`INSERT INTO catalog_commands (id, kind, source_id, status, updated_at, created_at)
			VALUES (?, 'deactivate_source', 's', ?, now() - make_interval(secs => ?), now() - make_interval(secs => ?))`,
			m.at(name, age), status, age.Seconds(), age.Seconds())
	}
	cmd("c-old-done", "done", 31*day)
	cmd("c-new-done", "done", 1*day)
	cmd("c-dead-200d", "dead", 200*day) // drives the backlog alert: kept
	cmd("c-old-pending", "pending", 200*day)
	return m
}

func TestSweepCrawlRetention_DeletesOnlyExpiredTerminalRows(t *testing.T) {
	g, _, ctx := setupCandidateDB(t)
	m := seedRetention(t, g)
	db := func(context.Context, bool) *gorm.DB { return g }

	res, err := repository.SweepCrawlRetention(ctx, db, repository.DefaultRetentionPolicy())
	require.NoError(t, err)
	require.EqualValues(t, 2, res["job_ingest_queue"])
	require.EqualValues(t, 1, res["job_ingest_events"])
	require.EqualValues(t, 1, res["crawl_jobs"])
	require.EqualValues(t, 1, res["crawl_runs"])
	require.EqualValues(t, 1, res["url_frontier"])
	require.EqualValues(t, 1, res["catalog_commands"])

	survivors := map[string]string{
		"job_ingest_queue":  "id IN " + m.in("q-new-processed", "q-new-dead", "q-old-pending"),
		"job_ingest_events": "event_id IN " + m.in("e-new"),
		"crawl_jobs":        "id IN " + m.in("j-old-running", "j-new-ok"),
		"crawl_runs":        "id IN " + m.in("r-new-done", "r-paused"),
		"url_frontier":      "url_id IN " + m.in("u-old-enqueued-recently-done", "u-old-pending", "u-old-parked", "u-new-done"),
		"catalog_commands":  "id IN " + m.in("c-new-done", "c-dead-200d", "c-old-pending"),
	}
	for table, where := range survivors {
		require.Equal(t, countWhere(t, g, table, "true"), countWhere(t, g, table, where), "%s survivors", table)
	}

	again, err := repository.SweepCrawlRetention(ctx, db, repository.DefaultRetentionPolicy())
	require.NoError(t, err)
	for table, n := range again {
		require.Zero(t, n, "second sweep is a no-op for %s", table)
	}
}

// One failing rule must not stop the others.
func TestSweepCrawlRetention_ContinuesPastAFailingRule(t *testing.T) {
	g, _, ctx := setupCandidateDB(t)
	m := seedRetention(t, g)
	require.NoError(t, g.Exec(`DROP TABLE crawl_runs`).Error)
	db := func(context.Context, bool) *gorm.DB { return g }

	res, err := repository.SweepCrawlRetention(ctx, db, repository.DefaultRetentionPolicy())
	require.ErrorContains(t, err, "crawl_runs")
	require.EqualValues(t, 1, res["url_frontier"], "rules after the failing one still ran")
	require.EqualValues(t, 1, res["catalog_commands"])
	require.Zero(t, countWhere(t, g, "url_frontier", "url_id IN "+m.in("u-old-done")))
}

func TestSweepCrawlRetention_BatchesAndBounds(t *testing.T) {
	g, _, ctx := setupCandidateDB(t)
	db := func(context.Context, bool) *gorm.DB { return g }
	for i := 0; i < 5; i++ {
		id := xid.NewWithTime(time.Now().Add(-40 * day)).String()
		require.NoError(t, g.Exec(`INSERT INTO job_ingest_queue (id, variant_id, source_id, idempotency_key, payload, status, processed_at)
			VALUES (?, ?, 's', ?, '{}', 'processed', now() - interval '40 days')`, id, id, id).Error)
	}
	p := repository.DefaultRetentionPolicy()
	p.BatchSize, p.MaxBatches = 2, 1
	res, err := repository.SweepCrawlRetention(ctx, db, p)
	require.NoError(t, err)
	require.EqualValues(t, 2, res["job_ingest_queue"], "one batch of two per sweep")

	p.MaxBatches = 10
	res, err = repository.SweepCrawlRetention(ctx, db, p)
	require.NoError(t, err)
	require.EqualValues(t, 3, res["job_ingest_queue"], "remaining rows across several batches")
}

// The ledger stays append-only for everything except an aged, flagged delete.
func TestIngestEventsGuard(t *testing.T) {
	g, _, ctx := setupCandidateDB(t)
	require.NoError(t, g.Exec(`INSERT INTO job_ingest_events (event_id, occurred_at, ingest_id, variant_id, source_id, event_type, attempt, details) VALUES
		('old', now() - interval '91 days', 'i', 'v', 's', 'enqueued', 0, '{}'),
		('new', now() - interval '1 day',   'i', 'v', 's', 'enqueued', 0, '{}')`).Error)

	require.Error(t, g.Exec(`DELETE FROM job_ingest_events WHERE event_id='old'`).Error, "no flag: refused")
	require.Error(t, g.Exec(`UPDATE job_ingest_events SET attempt=1 WHERE event_id='old'`).Error)
	require.Error(t, g.Exec(`TRUNCATE job_ingest_events`).Error)

	flagged := func(q string) error {
		return g.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec(`SET LOCAL stawi.retention_sweep = 'on'`).Error; err != nil {
				return err
			}
			return tx.Exec(q).Error
		})
	}
	require.Error(t, flagged(`DELETE FROM job_ingest_events WHERE event_id='new'`), "flagged but too young: refused")
	require.Error(t, flagged(`UPDATE job_ingest_events SET attempt=1 WHERE event_id='old'`), "flag never allows UPDATE")
	require.NoError(t, flagged(`DELETE FROM job_ingest_events WHERE event_id='old'`))
	require.EqualValues(t, 1, countWhere(t, g, "job_ingest_events", "true"))

	// The flag is transaction-local.
	require.Error(t, g.Exec(`DELETE FROM job_ingest_events WHERE event_id='new'`).Error)
}

// The retention flag only relaxes the crawl ingest ledger: matching's
// append-only ledgers share append_only_guard() and must stay strict even
// when both schemas live in one database (dev, tests).
func TestRetentionFlagDoesNotRelaxOtherLedgers(t *testing.T) {
	g, _, ctx := setupCandidateDB(t)
	var trig string
	require.NoError(t, g.Raw(`SELECT p.proname FROM pg_trigger tg JOIN pg_proc p ON p.oid = tg.tgfoid
		WHERE tg.tgname = 'job_ingest_events_append_only'`).Scan(&trig).Error)
	require.Equal(t, "ingest_events_guard", trig)

	var shared int64
	require.NoError(t, g.Raw(`SELECT count(*) FROM pg_trigger tg JOIN pg_proc p ON p.oid = tg.tgfoid
		WHERE p.proname = 'append_only_guard' AND tg.tgname = 'candidate_match_events_append_only'`).Scan(&shared).Error)
	require.EqualValues(t, 1, shared, "matching ledger still uses the shared strict guard")

	err := g.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SET LOCAL stawi.retention_sweep = 'on'`).Error; err != nil {
			return err
		}
		return tx.Exec(`DELETE FROM candidate_match_events`).Error
	})
	require.NoError(t, err, "no rows: the statement itself is allowed")
	require.Error(t, g.Exec(`TRUNCATE candidate_match_events`).Error)
}

// Keyset continuation: undeletable old rows interleaved with deletable ones
// do not stop or repeat the sweep.
func TestSweepCrawlRetention_KeysetSkipsUndeletableRows(t *testing.T) {
	g, _, ctx := setupCandidateDB(t)
	db := func(context.Context, bool) *gorm.DB { return g }
	base := time.Now().Add(-120 * day)
	for i := 0; i < 6; i++ {
		id := xid.NewWithTime(base.Add(time.Duration(i) * time.Hour)).String()
		status := "processed"
		if i%2 == 1 {
			status = "pending" // old live work, never deleted
		}
		require.NoError(t, g.Exec(`INSERT INTO job_ingest_queue (id, variant_id, source_id, idempotency_key, payload, status, processed_at)
			VALUES (?, ?, 's', ?, '{}', ?, now() - interval '100 days')`, id, id, id, status).Error)
	}
	p := repository.DefaultRetentionPolicy()
	p.BatchSize, p.MaxBatches = 1, 10
	res, err := repository.SweepCrawlRetention(ctx, db, p)
	require.NoError(t, err)
	require.EqualValues(t, 3, res["job_ingest_queue"])
	require.EqualValues(t, 3, countWhere(t, g, "job_ingest_queue", "status = 'pending'"))
	require.Zero(t, countWhere(t, g, "job_ingest_queue", "status = 'processed'"))
}

// The guard and the sweep must agree on "90 days" in any session time zone
// (a calendar interval shifts by an hour across DST).
func TestIngestEventsGuard_AgreesWithSweepAcrossDST(t *testing.T) {
	g, _, ctx := setupCandidateDB(t)
	id := xid.NewWithTime(time.Now().Add(-91 * day)).String()
	require.NoError(t, g.Exec(`INSERT INTO job_ingest_events (event_id, occurred_at, ingest_id, variant_id, source_id, event_type, attempt, details)
		VALUES (?, now() - make_interval(secs => 7776000) - interval '1 minute', 'i', 'v', 's', 'enqueued', 0, '{}')`, id).Error)
	err := g.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SET LOCAL TIME ZONE 'America/New_York'`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`SET LOCAL stawi.retention_sweep = 'on'`).Error; err != nil {
			return err
		}
		return tx.Exec(`DELETE FROM job_ingest_events WHERE occurred_at < now() - make_interval(secs => 7776000)`).Error
	})
	require.NoError(t, err, "a row the sweep selects must be deletable under the guard")
	require.Zero(t, countWhere(t, g, "job_ingest_events", "true"))
}
