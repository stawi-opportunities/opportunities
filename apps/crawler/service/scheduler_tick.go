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

	"stawi.jobs/pkg/domain"
	eventsv1 "stawi.jobs/pkg/events/v1"
)

// SourceLister is the narrow slice of *repository.SourceRepository the
// scheduler-tick handler needs. Kept here (rather than importing the
// full repo type) so unit tests can swap in a fake without touching
// Postgres.
type SourceLister interface {
	ListDue(ctx context.Context, now time.Time, limit int) ([]*domain.Source, error)
	UpdateNextCrawl(ctx context.Context, id string, next, verified time.Time, health float64) error
}

// Admitter is the slice of *backpressure.Gate the handler needs.
type Admitter interface {
	Admit(ctx context.Context, topic string, want int) (int, time.Duration)
}

// schedulerTickResponse is the JSON body returned to Trustage. Kept
// exported-enough-for-tests via the lowercase struct + json tags.
type schedulerTickResponse struct {
	OK         bool `json:"ok"`
	Considered int  `json:"considered"`
	Admitted   int  `json:"admitted"`
	Dispatched int  `json:"dispatched"`

	// WaitHintSec is set when the gate refused and wants the caller
	// to back off. Trustage logs it; it does not re-drive retries.
	WaitHintSec int `json:"wait_hint_sec,omitempty"`
}

// SchedulerTickHandler returns an HTTP handler that implements the
// scheduler.tick contract from §6.1 of the design:
//
//  1. Enumerate due sources (up to limit) via SourceLister.ListDue.
//  2. Ask the Admitter how many of those N the pipeline can accept.
//  3. For the admitted K: emit one crawl.requests.v1 per source and
//     stamp next_crawl_at forward so a concurrent tick on another pod
//     can't double-admit.
//  4. Leave the deferred (N-K) sources untouched — the next tick
//     retries them.
//
// The handler is idempotent over the same wallclock window: if the
// Trustage-configured cadence is 30 s and the UpdateNextCrawl adds the
// source's CrawlIntervalSec (typically 60–7200 s), a second tick 30 s
// later will see no rows in ListDue for the same set of sources.
func SchedulerTickHandler(svc *frame.Service, lister SourceLister, admit Admitter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		ctx := r.Context()
		log := util.Log(ctx)
		now := time.Now().UTC()

		sources, err := lister.ListDue(ctx, now, 500)
		if err != nil {
			log.WithError(err).Error("scheduler/tick: ListDue failed")
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}

		granted, wait := admit.Admit(ctx, eventsv1.TopicCrawlRequests, len(sources))

		resp := schedulerTickResponse{
			OK:         true,
			Considered: len(sources),
			Admitted:   granted,
		}
		if wait > 0 {
			resp.WaitHintSec = int(wait / time.Second)
		}

		evtMgr := svc.EventsManager()
		if evtMgr == nil {
			http.Error(w, `{"error":"events manager unavailable"}`, http.StatusServiceUnavailable)
			return
		}

		for i := 0; i < granted && i < len(sources); i++ {
			src := sources[i]
			env := eventsv1.NewEnvelope(eventsv1.TopicCrawlRequests, eventsv1.CrawlRequestV1{
				RequestID: xid.New().String(),
				SourceID:  src.ID,
				Mode:      "auto",
				Attempt:   1,
			})
			if emitErr := evtMgr.Emit(ctx, eventsv1.TopicCrawlRequests, env); emitErr != nil {
				log.WithError(emitErr).WithField("source_id", src.ID).Warn("scheduler/tick: emit failed")
				continue
			}

			// Stamp next_crawl_at forward so a concurrent pod's tick
			// doesn't re-admit this source. Preserve LastVerifiedAt
			// (only the reachability probe mutates it) and HealthScore.
			next := now.Add(time.Duration(src.CrawlIntervalSec) * time.Second)
			var lastVerified time.Time
			if src.LastVerifiedAt != nil {
				lastVerified = *src.LastVerifiedAt
			}
			if stampErr := lister.UpdateNextCrawl(ctx, src.ID, next, lastVerified, src.HealthScore); stampErr != nil {
				log.WithError(stampErr).WithField("source_id", src.ID).Warn("scheduler/tick: stamp next_crawl_at failed")
			}
			resp.Dispatched++
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
