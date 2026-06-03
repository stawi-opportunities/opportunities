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
)

// SourceCrawlGetter is the narrow source lookup the per-source crawl endpoint
// needs (satisfied by *repository.SourceRepository).
type SourceCrawlGetter interface {
	GetByID(ctx context.Context, id string) (*domain.Source, error)
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
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "dispatched": 0, "reason": "source not active"})
			return
		}

		// Backpressure gate: one crawl == one expected ingested fan-out.
		granted, wait := admit.Admit(ctx, eventsv1.TopicCrawlRequests, 1)
		if granted < 1 {
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
		log.WithField("source_id", src.ID).Info("source-crawl: dispatched")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "dispatched": 1, "source_id": src.ID})
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
