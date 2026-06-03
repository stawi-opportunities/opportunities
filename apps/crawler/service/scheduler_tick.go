package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"
	"github.com/rs/xid"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/freshness"
)

// SourceLister is the narrow slice of *repository.SourceRepository the
// scheduler-tick handler needs. Kept here (rather than importing the
// full repo type) so unit tests can swap in a fake without touching
// Postgres.
type SourceLister interface {
	ListDue(ctx context.Context, now time.Time, limit int) ([]*domain.Source, error)
	UpdateNextCrawl(ctx context.Context, id string, next, verified time.Time, health float64) error
}

// AdaptiveScorer is the optional interface — when a SourceLister also
// satisfies this, the tick handler refreshes the crawl_signals
// materialized view and recomputes per-source scores before listing
// due rows. Concrete *repository.SourceRepository implements it; test
// fakes can omit it and the tick handler degrades gracefully (legacy
// fixed-interval scheduling continues to work).
type AdaptiveScorer interface {
	RefreshSignals(ctx context.Context) error
	LoadSignals(ctx context.Context) (map[string]freshness.SourceSignals, error)
	ListAll(ctx context.Context) ([]*domain.Source, error)
	UpdateScoreAndNextCrawl(ctx context.Context, sourceID string, score float64, nextCrawlAt time.Time) error
}

// recomputeScores refreshes the crawl_signals view, recomputes the
// freshness score for every source with signals, and persists the
// derived next_crawl_at. Best-effort: any failure logs a warning and
// returns — the legacy ListDue path still fires on the existing
// next_crawl_at so the tick degrades to "stale signals" rather than
// "skipped tick". Tier is hard-coded to 2 (neutral) until the
// Source.Tier column lands; see plan note for Plan B2.
func recomputeScores(ctx context.Context, scorer AdaptiveScorer, now time.Time) {
	log := util.Log(ctx)
	if err := scorer.RefreshSignals(ctx); err != nil {
		log.WithError(err).Warn("scheduler/tick: REFRESH crawl_signals failed; using stale signals")
		// Continue — LoadSignals reads the prior refresh's snapshot.
	}
	signals, err := scorer.LoadSignals(ctx)
	if err != nil {
		log.WithError(err).Warn("scheduler/tick: LoadSignals failed; skipping score recompute")
		return
	}
	if len(signals) == 0 {
		return
	}
	sources, err := scorer.ListAll(ctx)
	if err != nil {
		log.WithError(err).Warn("scheduler/tick: ListAll failed; skipping score recompute")
		return
	}
	updated := 0
	for _, src := range sources {
		sig, ok := signals[src.ID]
		if !ok {
			continue // source has no signals row yet
		}
		// Tier is not yet on the Source struct; pass neutral (2) so
		// the freshness package's tier nudge is a no-op. Plan B2 will
		// surface a real tier and the call site flips to src.Tier.
		score := freshness.Score(sig, 2, now)
		// Update the freshness SCORE only — do NOT recompute next_crawl_at
		// here. next_crawl_at is owned by the dispatch path (which stamps
		// now+CrawlIntervalSec right after emitting a crawl request) and the
		// page-completed path (after a crawl finishes) — both anchored to a
		// real crawl event, so a source legitimately becomes due once its
		// interval elapses since the last crawl.
		//
		// The previous code reset next_crawl_at to freshness.NextCrawlAt(
		// score, now, ...) = now+interval on EVERY tick. The tick fires every
		// minute but the minimum interval is 15 min, so the value perpetually
		// receded into the future and ListDue(now) never saw a due source —
		// the whole pipeline silently starved of input from the moment this
		// recompute shipped (commit 4b6183c, 2026-05-29). Preserve the
		// source's existing next_crawl_at; only the score changes per tick.
		if upErr := scorer.UpdateScoreAndNextCrawl(ctx, src.ID, score, src.NextCrawlAt); upErr != nil {
			log.WithError(upErr).WithField("source_id", src.ID).Warn("scheduler/tick: score update failed")
			continue
		}
		updated++
	}
	log.WithField("scored", updated).WithField("considered", len(sources)).Debug("scheduler/tick: scores recomputed")
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
// recomputeInFlight serializes the out-of-band score refresh so overlapping
// scheduler ticks don't launch concurrent materialized-view refreshes.
var recomputeInFlight sync.Mutex

func SchedulerTickHandler(svc *frame.Service, lister SourceLister, admit Admitter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		ctx := r.Context()
		log := util.Log(ctx)
		now := time.Now().UTC()

		// Adaptive recrawl (Plan D3): if the SourceLister also supports
		// the AdaptiveScorer surface, refresh signals + recompute scores
		// before listing due rows. The view refresh is CONCURRENT, the
		// score recompute is best-effort, and any failure logs a warning
		// rather than aborting the tick — the legacy fixed-interval path
		// still works because ListDue keys on next_crawl_at which is
		// pre-seeded by the backfill migration.
		if scorer, ok := lister.(AdaptiveScorer); ok {
			// Refresh scores OUT OF BAND. recomputeScores does a CONCURRENT
			// materialized-view refresh plus a per-source UPDATE for every
			// source — tens of seconds of work. Running it inline on the
			// request context meant a slow refresh got the context canceled
			// by Trustage's HTTP timeout BEFORE ListDue/dispatch ran, so the
			// tick recomputed scores but emitted zero crawl requests and the
			// pipeline saw no input. Scores feed observability only; they do
			// not gate ListDue or admission. Detach from the request context
			// (request completion must not cancel it) and TryLock so
			// overlapping ticks don't launch concurrent refreshes.
			if recomputeInFlight.TryLock() {
				bgCtx := context.WithoutCancel(ctx)
				go func() {
					defer recomputeInFlight.Unlock()
					rctx, cancel := context.WithTimeout(bgCtx, 90*time.Second)
					defer cancel()
					recomputeScores(rctx, scorer, time.Now().UTC())
				}()
			}
		}

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

		// tickMinute is the partition key for the idempotency contract:
		// every source admitted in this sweep gets the same minute-
		// truncated timestamp, so concurrent ticks (or NATS redeliveries)
		// collide on (source_id, tick_minute) and the crawl handler
		// reuses the existing crawl_jobs row instead of inserting a dup.
		tickMinute := now.UTC().Truncate(time.Minute).Format(time.RFC3339)
		for i := 0; i < granted && i < len(sources); i++ {
			src := sources[i]
			env := eventsv1.NewEnvelope(eventsv1.TopicCrawlRequests, eventsv1.CrawlRequestV1{
				RequestID:      xid.New().String(),
				SourceID:       src.ID,
				IdempotencyKey: fmt.Sprintf("%s:%s", src.ID, tickMinute),
				ScheduledAt:    now,
				Mode:           "auto",
				Attempt:        1,
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
