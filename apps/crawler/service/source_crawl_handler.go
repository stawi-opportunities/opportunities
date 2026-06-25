package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"
	"github.com/rs/xid"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/telemetry"
)

// SourceCrawlGetter is the narrow source lookup the per-source crawl endpoint
// needs (satisfied by *repository.SourceRepository).
type SourceCrawlGetter interface {
	GetByID(ctx context.Context, id string) (*domain.Source, error)
}

// Admitter is the backpressure-gate slice the crawl dispatch needs: given a
// topic and a desired count, it returns how many are admitted and a wait hint.
// Satisfied by *backpressure.Gate.
type Admitter interface {
	Admit(ctx context.Context, topic string, want int) (int, time.Duration)
}

// SourceCrawlHandler returns POST /admin/sources/{id}/crawl: emit exactly one
// crawl.requests.v1 for the given source. This is the per-source counterpart to
// the central scheduler tick — each source's own Trustage schedule fires this
// at the source's cadence, so there is no central ListDue / next_crawl_at poll.
//
// Backpressure is still honoured: the handler asks the gate for one slot and,
// if denied, returns 429 + Retry-After so Trustage's step retry/backoff
// reschedules instead of piling onto a saturated pipeline.
func SourceCrawlHandler(svc *frame.Service, getter SourceCrawlGetter, admit Admitter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		id := r.PathValue("id")
		if id == "" {
			http.Error(w, `{"error":"missing source id"}`, http.StatusBadRequest)
			return
		}
		ctx := r.Context()
		log := util.Log(ctx)
		now := time.Now().UTC()

		src, err := getter.GetByID(ctx, id)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		if src == nil {
			http.Error(w, `{"error":"source not found"}`, http.StatusNotFound)
			return
		}
		// Don't crawl a source that isn't active — the schedule should have
		// been archived, but guard against a race (schedule fires between
		// disable and archive).
		if !scheduleActive(src) {
			telemetry.RecordCrawlDispatch("skipped", "schedule")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "dispatched": 0, "reason": "source not active"})
			return
		}

		// Backpressure gate: one crawl == one expected ingested fan-out.
		granted, wait := admit.Admit(ctx, eventsv1.TopicCrawlRequests, 1)
		if granted < 1 {
			telemetry.RecordCrawlDispatch("denied", "schedule")
			waitSec := int(wait / time.Second)
			if waitSec > 0 {
				w.Header().Set("Retry-After", fmt.Sprintf("%d", waitSec))
			}
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "reason": "backpressure", "retry_after_sec": waitSec})
			return
		}

		evtMgr := svc.EventsManager()
		if evtMgr == nil {
			http.Error(w, `{"error":"events manager unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		tickMinute := now.Truncate(time.Minute).Format(time.RFC3339)
		env := eventsv1.NewEnvelope(eventsv1.TopicCrawlRequests, eventsv1.CrawlRequestV1{
			RequestID:      xid.New().String(),
			SourceID:       src.ID,
			IdempotencyKey: fmt.Sprintf("%s:%s", src.ID, tickMinute),
			ScheduledAt:    now,
			Mode:           "auto",
			Attempt:        1,
		})
		if emitErr := evtMgr.Emit(ctx, eventsv1.TopicCrawlRequests, env); emitErr != nil {
			log.WithError(emitErr).WithField("source_id", src.ID).Error("source-crawl: emit failed")
			http.Error(w, fmt.Sprintf(`{"error":%q}`, emitErr.Error()), http.StatusInternalServerError)
			return
		}
		telemetry.RecordCrawlDispatch("emitted", "schedule")
		log.WithField("source_id", src.ID).Info("source-crawl: dispatched")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "dispatched": 1, "source_id": src.ID})
	}
}

// CrawlOverdueLister lists sources whose next_crawl_at is past the given
// time (satisfied by *repository.SourceRepository.ListDue).
type CrawlOverdueLister interface {
	ListDue(ctx context.Context, now time.Time, limit int) ([]*domain.Source, error)
}

// NextCrawlBumper stamps the optimistic dispatch lease (satisfied by
// *repository.SourceRepository.Update).
type NextCrawlBumper interface {
	Update(ctx context.Context, id string, fields map[string]any) error
}

// OverdueSlack is how far past due a source must be before the catch-up
// sweep dispatches it. The per-source Trustage cron owns the normal path;
// the sweep exists for the failure modes that lose a tick entirely — a
// backpressure-denied tick (Trustage's in-step retries span seconds, the
// next cron fire is up to a full interval away), a per-source workflow
// that was never created/activated (the 2026-06-03 outage), or Trustage
// downtime. One hour of slack guarantees the sweep never races a healthy
// cron tick, while capping tick-loss damage at ~1h instead of 12h+.
const OverdueSlack = time.Hour

// CrawlOverdueHandler returns POST /admin/sources/crawl-overdue: the
// catch-up sweep behind the per-source schedules. Each tick it lists up
// to `batch` sources at least OverdueSlack past due and emits one
// crawl.requests.v1 per source (backpressure-gated, stopping the batch
// when the gate closes), then pushes each dispatched source's
// next_crawl_at out by its interval so it isn't re-dispatched before the
// crawl completes (page-completed later stamps the real cadence). Driven
// by a static-synced Trustage cron (definitions/trustage/
// source-crawl-overdue.json) — static workflows fire even when dynamic
// per-source ones don't, which is exactly the failure mode this guards.
func CrawlOverdueHandler(svc *frame.Service, lister CrawlOverdueLister, bumper NextCrawlBumper, admit Admitter, batch int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		ctx := r.Context()
		log := util.Log(ctx)
		now := time.Now().UTC()
		if batch <= 0 {
			batch = 25
		}

		due, err := lister.ListDue(ctx, now.Add(-OverdueSlack), batch)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		evtMgr := svc.EventsManager()
		if evtMgr == nil {
			http.Error(w, `{"error":"events manager unavailable"}`, http.StatusServiceUnavailable)
			return
		}

		dispatched, throttled := 0, false
		for _, src := range due {
			if !scheduleActive(src) {
				telemetry.RecordCrawlDispatch("skipped", "overdue")
				continue
			}
			if granted, _ := admit.Admit(ctx, eventsv1.TopicCrawlRequests, 1); granted < 1 {
				telemetry.RecordCrawlDispatch("denied", "overdue")
				throttled = true
				break // pipeline saturated — leave the rest for the next tick
			}
			tickMinute := now.Truncate(time.Minute).Format(time.RFC3339)
			env := eventsv1.NewEnvelope(eventsv1.TopicCrawlRequests, eventsv1.CrawlRequestV1{
				RequestID:      xid.New().String(),
				SourceID:       src.ID,
				IdempotencyKey: fmt.Sprintf("%s:%s", src.ID, tickMinute),
				ScheduledAt:    now,
				Mode:           "auto",
				Attempt:        1,
			})
			if emitErr := evtMgr.Emit(ctx, eventsv1.TopicCrawlRequests, env); emitErr != nil {
				log.WithError(emitErr).WithField("source_id", src.ID).Error("crawl-overdue: emit failed")
				continue
			}
			telemetry.RecordCrawlDispatch("emitted", "overdue")
			// Optimistic lease: floor at MinCrawlIntervalHours so a slow or
			// failed crawl can't be re-dispatched every tick.
			intervalSec := max(src.CrawlIntervalSec, MinCrawlIntervalHours*3600)
			next := now.Add(time.Duration(intervalSec) * time.Second)
			if uerr := bumper.Update(ctx, src.ID, map[string]any{"next_crawl_at": next}); uerr != nil {
				log.WithError(uerr).WithField("source_id", src.ID).Warn("crawl-overdue: bump next_crawl_at failed")
			}
			dispatched++
		}

		if dispatched > 0 {
			// Overdue dispatches mean the primary per-source schedules lost
			// ticks — surface loudly so it can't silently become the norm.
			log.WithField("dispatched", dispatched).WithField("due", len(due)).
				Error("crawl-overdue: per-source schedules lost ticks; catch-up sweep dispatched")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "dispatched": dispatched, "due": len(due), "throttled": throttled,
		})
	}
}

// SourceScheduleReconciler lists every source for the schedule reconcile pass.
type SourceScheduleReconciler interface {
	ListAll(ctx context.Context) ([]*domain.Source, error)
}

// ScheduleReconcileHandler returns POST /admin/sources/schedules/reconcile:
// drive every source's Trustage schedule to match its status (active → ensure,
// inactive → archive). Trustage fires this periodically as the drift backstop;
// the per-mutation hooks keep it instant in the common path. No-op (200) when
// the Trustage client isn't configured.
func ScheduleReconcileHandler(lister SourceScheduleReconciler, client WorkflowClient, crawlBaseURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		ctx := r.Context()
		if client == nil || crawlBaseURL == "" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "skipped": "trustage not configured"})
			return
		}
		sources, err := lister.ListAll(ctx)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		ensured, archived, failed := ReconcileSourceSchedules(ctx, client, sources, crawlBaseURL)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": failed == 0, "ensured": ensured, "archived": archived, "failed": failed,
		})
	}
}
