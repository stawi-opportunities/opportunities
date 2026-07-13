package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/pitabwire/frame/v2"
	"github.com/pitabwire/util"
	"github.com/rs/xid"

	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/recipeconn"
	"github.com/stawi-opportunities/opportunities/pkg/crawlaccept"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/frontier"
	"github.com/stawi-opportunities/opportunities/pkg/jobqueue"
	"github.com/stawi-opportunities/opportunities/pkg/normalize"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/recipe"
	"github.com/stawi-opportunities/opportunities/pkg/recipe/stock"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
	"github.com/stawi-opportunities/opportunities/pkg/telemetry"
)

// SourceGetter is the narrow repository slice the crawl-request handler
// needs. Satisfied by *repository.SourceRepository in production.
type SourceGetter interface {
	GetByID(ctx context.Context, id string) (*domain.Source, error)
}

// SourceStatusSetter lets the handler pause a source it can't crawl (no
// connector and no recipe). Satisfied by *repository.SourceRepository.
type SourceStatusSetter interface {
	SetStatus(ctx context.Context, id string, status domain.SourceStatus) error
}

// CrawlRunStore is the crawl_runs state-machine slice the handler drives
// (satisfied by *repository.CrawlRunRepository). It is an interface so the
// handler can be unit-tested without a database.
type CrawlRunStore interface {
	StartRun(ctx context.Context, sourceID string, scheduledAt time.Time, owner string, leaseTTL time.Duration) (*domain.CrawlRun, bool, error)
	Claim(ctx context.Context, id, owner string, leaseTTL time.Duration) (*domain.CrawlRun, bool, error)
	Progress(ctx context.Context, id string, cursor json.RawMessage, pageIdx int, lastURL string, leaseTTL time.Duration) error
	Yield(ctx context.Context, id string, cursor json.RawMessage, pageIdx int, lastURL string, leaseTTL time.Duration, dFound, dEmitted, dRejected int) error
	Complete(ctx context.Context, id string, cursor json.RawMessage, pageIdx int, lastURL string, dFound, dEmitted, dRejected int) error
	Fail(ctx context.Context, id, code, message string, dFound, dEmitted, dRejected int) error
}

// CrawlRequestDeps bundles the handler's collaborators so construction
// stays one-shot and tests can inject fakes without ceremony.
type CrawlRequestDeps struct {
	Svc *frame.Service
	// IngestQueue is the required durable PostgreSQL processing boundary.
	IngestQueue *jobqueue.Store
	Sources     SourceGetter
	// StatusSetter (optional) pauses a source the handler can't crawl — no
	// connector and no recipe (e.g. an individual ATS board whose connector was
	// migrated to an aggregate recipe). nil disables auto-pause.
	StatusSetter SourceStatusSetter
	Registry   *connectors.Registry
	Kinds      *opportunity.Registry // opportunity-kind registry; required by Verify
	Normalizer *normalize.Normalizer // nil → fall back to raw ExternalToVariant (no geocoder)
	// PageFetcher is the HTTP client used by the recipe executor for
	// structured page fetches. Not used for LLM enrichment.
	PageFetcher *httpx.Client
	// CrawlRepo writes the crawl_jobs audit ledger.
	// nil disables ledger writes (test paths). Errors propagate — a
	// Postgres outage MUST fail the crawl, otherwise the ledger silently
	// diverges from reality.
	CrawlRepo *repository.CrawlRepository
	// RunRepo drives the crawl_runs state machine: it opens a run per source
	// (single-flight), holds the per-slice lease, and persists the resume
	// cursor after every page so a crash, redeploy, or bounded-slice yield
	// resumes from the last page instead of restarting from zero. nil (unit
	// tests) degrades the crawl to a single-shot, non-resumable drain.
	RunRepo CrawlRunStore

	// Admitter is the backpressure gate. Continuation re-enqueues pass through
	// it so a deep crawl paces itself when the pipeline is saturated. nil emits
	// continuations unconditionally (test paths).
	Admitter Admitter

	// Owner identifies this process as the holder of a run's lease (e.g. the
	// pod name). Two consumers can't both drive a run's slice concurrently.
	Owner string

	// SliceMaxPages / SliceMaxSeconds bound one slice (one NATS message): the
	// slice ends after whichever is hit first, then the handler yields and
	// self-re-enqueues. Zero falls back to 50 pages / 120s.
	SliceMaxPages   int
	SliceMaxSeconds int

	// RunLeaseTTLSec is the per-source run lease, renewed every page. Zero
	// falls back to 5 minutes.
	RunLeaseTTLSec int

	// RunStuckMaxAttempts fails a run after this many slices without completing
	// (a backstop against a run that never converges). Zero disables the cap.
	RunStuckMaxAttempts int

	// IngestMaxPending and IngestMaxOldestAge stop a crawl slice after the
	// current page has been durably enqueued, allowing processing to catch up.
	IngestMaxPending   int64
	IngestMaxOldestAge time.Duration

	// Frontier wires the D2 URL-frontier path. When non-nil AND the
	// source has FrontierEnabled=true, the crawl handler enqueues
	// discovered URLs into the frontier instead of running the
	// extract+emit pipeline in-line. The frontier-worker then handles
	// the per-URL fetch + extract under per-host politeness.
	//
	// nil disables URL-level scheduling and keeps source-level extraction.
	Frontier frontier.Frontier

	// RecipeRepo, when set, supplies per-source extraction recipes. A source
	// with an active recipe crawls via the deterministic recipe Executor
	// instead of its registered connector.
	RecipeRepo *repository.RecipeRepository

	// RecipeEnabled gates the recipe path globally. When false, sources keep
	// crawling via their registered connector even if they have a recipe — the
	// rollout kill-switch (RECIPE_ENABLED).
	RecipeEnabled bool
}

// useRecipePath reports whether a crawl should use the deterministic recipe
// Executor: only when the engine is globally enabled and the source has one.
func useRecipePath(enabled bool, rec *recipe.Recipe) bool {
	return enabled && rec != nil
}

// bootstrapStockRecipe persists a bundled stock recipe onto the source so
// subsequent crawls use the DB row (data) rather than re-resolving by type/URL.
// Best-effort: activation failure does not block this crawl (rec is already in hand).
func (h *CrawlRequestHandler) bootstrapStockRecipe(ctx context.Context, sourceID, name string, rec *recipe.Recipe) {
	if h.deps.RecipeRepo == nil || rec == nil {
		return
	}
	// Skip if already has a recipe (race with concurrent bootstrap).
	if active, err := h.deps.RecipeRepo.Active(ctx, sourceID); err == nil && active != nil {
		return
	}
	if err := h.deps.RecipeRepo.Activate(ctx, sourceID, rec, 1.0, "stock:"+name, map[string]any{
		"source": "stock_bootstrap", "stock": name,
	}); err != nil {
		util.Log(ctx).WithError(err).WithField("source_id", sourceID).WithField("stock", name).
			Warn("crawl.request: stock recipe activate failed")
		return
	}
	util.Log(ctx).WithField("source_id", sourceID).WithField("stock", name).
		Info("crawl.request: stock recipe activated")
}

// CrawlRequestHandler consumes jobs.crawl.requests.v1, runs the
// connector iterator for the source, parses content in memory, optionally
// extracts structured jobs and enqueues accepted records.
//
//   - one opportunities.variants.ingested.v1 per accepted job
//   - exactly one crawl.page.completed.v1 summary per request
type CrawlRequestHandler struct {
	deps CrawlRequestDeps
}

// NewCrawlRequestHandler wires the handler. Deps are captured by value
// because nothing in the struct is mutable between calls.
func NewCrawlRequestHandler(deps CrawlRequestDeps) *CrawlRequestHandler {
	if deps.Owner == "" {
		// A per-process lease owner so two consumers never drive the same run's
		// slice concurrently. Hostname-ish identity is enough; uniqueness across
		// pods matters, not human readability.
		deps.Owner = "crawler-" + xid.New().String()
	}
	return &CrawlRequestHandler{deps: deps}
}

// Name implements frame.EventI.
func (h *CrawlRequestHandler) Name() string { return eventsv1.TopicCrawlRequests }

// PayloadType returns a pointer to json.RawMessage so Frame skips
// payload-specific deserialization; Execute does the typed decode.
func (h *CrawlRequestHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

// Validate is a cheap shape check — an empty payload is a bug and
// should dead-letter.
func (h *CrawlRequestHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("crawl.request: empty payload")
	}
	return nil
}

// Execute processes one crawl request. Error returns trigger Frame
// redelivery. Non-retryable conditions (unknown source, unknown
// connector type) return nil after emitting a page-completed event
// with error_code set — they are data-plane outcomes, not transport
// failures.
func (h *CrawlRequestHandler) Execute(ctx context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil {
		return errors.New("crawl.request: wrong payload type")
	}
	var env eventsv1.Envelope[eventsv1.CrawlRequestV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return fmt.Errorf("crawl.request: decode envelope: %w", err)
	}
	req := env.Payload

	log := util.Log(ctx).
		WithField("request_id", req.RequestID).
		WithField("source_id", req.SourceID)

	evtMgr := h.deps.Svc.EventsManager()
	if evtMgr == nil {
		return errors.New("crawl.request: events manager unavailable")
	}

	src, err := h.deps.Sources.GetByID(ctx, req.SourceID)
	if err != nil {
		// Transient; let Frame redeliver.
		return fmt.Errorf("crawl.request: GetByID: %w", err)
	}
	if src == nil {
		h.emitCompleted(ctx, eventsv1.CrawlPageCompletedV1{
			RequestID: req.RequestID,
			SourceID:  req.SourceID,
			ErrorCode: "source_not_found",
		})
		log.Warn("crawl.request: source not found")
		return nil
	}
	// Defensive default: in-code Source construction (tests, CLI tools,
	// edge cases) can leave Kinds empty even though the DB column
	// defaults to '{job}'. Without this, Verify rejects every record
	// with the operator-confusing "kind \"job\" not declared by source
	// (declared: [])" message. Treat empty as the conservative single-
	// kind default and warn so ops notice the misconfiguration.
	if len(src.Kinds) == 0 {
		util.Log(ctx).WithField("source_id", src.ID).
			Warn("crawl.request: source has empty Kinds; defaulting to [job]")
		src.Kinds = []string{"job"}
	}
	if src.Status != domain.SourceActive && src.Status != domain.SourceDegraded {
		h.emitCompleted(ctx, eventsv1.CrawlPageCompletedV1{
			RequestID: req.RequestID,
			SourceID:  req.SourceID,
			URL:       src.BaseURL,
			ErrorCode: "source_not_eligible",
		})
		return nil
	}

	// Resolve the crawl path: prefer the source's active extraction recipe
	// (data-driven). If none, fall back to a bundled stock recipe for known
	// public APIs, else a generic engine connector (schema.org / sitemap /
	// ATS). Site-specific Go packages are not used.
	var (
		rec        *recipe.Recipe
		usedRecipe bool
		conn       connectors.Connector
	)
	if h.deps.RecipeEnabled && h.deps.RecipeRepo != nil {
		r, rErr := h.deps.RecipeRepo.Active(ctx, src.ID)
		if rErr != nil {
			log.WithError(rErr).Warn("crawl.request: recipe lookup failed")
		} else if useRecipePath(h.deps.RecipeEnabled, r) {
			rec = r
			usedRecipe = true
		}
	}
	if !usedRecipe && h.deps.RecipeEnabled {
		if name, stockRec := stock.LookupByBaseURL(src.BaseURL); stockRec != nil {
			rec = stockRec
			usedRecipe = true
			h.bootstrapStockRecipe(ctx, src.ID, name, stockRec)
		}
	}
	if !usedRecipe {
		if domain.RequiresRecipe(src.Type) {
			log.WithField("source_type", src.Type).
				Warn("crawl.request: engine requires a recipe; none installed")
			if h.deps.StatusSetter != nil {
				_ = h.deps.StatusSetter.SetStatus(ctx, src.ID, domain.SourcePaused)
			}
			h.emitCompleted(ctx, eventsv1.CrawlPageCompletedV1{
				RequestID: req.RequestID,
				SourceID:  req.SourceID,
				URL:       src.BaseURL,
				ErrorCode: "recipe_required",
			})
			return nil
		}
		c, ok := h.deps.Registry.Get(src.Type)
		if !ok {
			if h.deps.StatusSetter != nil {
				if perr := h.deps.StatusSetter.SetStatus(ctx, src.ID, domain.SourcePaused); perr != nil {
					log.WithError(perr).Warn("crawl.request: auto-pause of engine-less source failed")
				} else {
					log.WithField("source_type", src.Type).
						Warn("crawl.request: no engine — paused source")
				}
			} else {
				log.WithField("source_type", src.Type).Warn("crawl.request: no engine")
			}
			h.emitCompleted(ctx, eventsv1.CrawlPageCompletedV1{
				RequestID: req.RequestID,
				SourceID:  req.SourceID,
				URL:       src.BaseURL,
				ErrorCode: "connector_not_registered",
			})
			return nil
		}
		conn = c
	}

	// Resume state machine: resolve the crawl_runs row that owns this slice. A
	// continuation (RunID set) claims its run's lease — losing the claim drops
	// the redelivery. A scheduled tick opens a fresh run, single-flight against
	// the partial unique index: if a run is already active for the source, this
	// duplicate tick is dropped. When RunRepo is unwired (unit tests) the crawl
	// degrades to a single-shot, non-resumable drain.
	leaseTTL := time.Duration(h.deps.RunLeaseTTLSec) * time.Second
	if leaseTTL <= 0 {
		leaseTTL = 5 * time.Minute
	}
	var run *domain.CrawlRun
	if h.deps.RunRepo != nil {
		if req.RunID != "" {
			claimed, ok, cErr := h.deps.RunRepo.Claim(ctx, req.RunID, h.deps.Owner, leaseTTL)
			if cErr != nil {
				return fmt.Errorf("crawl.request: claim run: %w", cErr)
			}
			if !ok || claimed == nil {
				log.WithField("run_id", req.RunID).Info("crawl.request: run already owned or closed; dropping continuation")
				return nil
			}
			run = claimed
		} else {
			opened, started, sErr := h.deps.RunRepo.StartRun(ctx, src.ID, req.ScheduledAt, h.deps.Owner, leaseTTL)
			if sErr != nil {
				return fmt.Errorf("crawl.request: start run: %w", sErr)
			}
			if !started {
				log.Info("crawl.request: a run is already active for this source; dropping duplicate scheduled tick")
				return nil
			}
			run = opened
		}
	}

	// Audit ledger: open a crawl_jobs row per slice. Fresh ticks dedupe
	// redeliveries on (source, scheduled tick); continuations key on their
	// unique RequestID so each slice gets its own audit row.
	crawlJob := &domain.CrawlJob{
		SourceID:       src.ID,
		ScheduledAt:    req.ScheduledAt,
		Status:         domain.CrawlScheduled,
		Attempt:        1,
		IdempotencyKey: req.IdempotencyKey,
	}
	if crawlJob.IdempotencyKey == "" {
		if req.RunID != "" {
			crawlJob.IdempotencyKey = req.RequestID
		} else {
			crawlJob.IdempotencyKey = fmt.Sprintf("%s:%s", src.ID, req.ScheduledAt.Format(time.RFC3339))
		}
	}
	if crawlJob.ScheduledAt.IsZero() {
		crawlJob.ScheduledAt = time.Now().UTC()
	}

	if h.deps.CrawlRepo != nil {
		if err := h.deps.CrawlRepo.Create(ctx, crawlJob); err != nil {
			// Likely a unique-constraint violation from a re-delivery.
			// Fall back to the existing row so we don't double-insert.
			if existing, lookupErr := h.deps.CrawlRepo.GetByIdempotencyKey(ctx, crawlJob.IdempotencyKey); lookupErr == nil && existing != nil {
				crawlJob = existing
			} else {
				return fmt.Errorf("crawl.request: open crawl_jobs row: %w", err)
			}
		}
		if err := h.deps.CrawlRepo.Start(ctx, crawlJob.ID); err != nil {
			return fmt.Errorf("crawl.request: mark started: %w", err)
		}
	}

	// Build the iterator, seeded from the run's persisted cursor so a recipe
	// source — like every source now — resumes mid-pagination instead of
	// restarting from page 0 / tenant 0 on each slice or after a crash.
	var (
		resumeCursor  json.RawMessage
		resumePageIdx int
		resumeLastURL string
	)
	if run != nil {
		resumeCursor, resumePageIdx, resumeLastURL = run.Cursor, run.PageIdx, run.LastURL
	}
	var iter connectors.CrawlIterator
	if usedRecipe {
		exec := recipe.NewExecutor(rec, recipe.NewHTTPFetcher(h.deps.PageFetcher))
		if len(resumeCursor) > 0 {
			ri, e := recipeconn.NewConnectorIteratorResume(exec, *src, resumeCursor)
			if e != nil {
				log.WithError(e).Warn("crawl.request: bad run cursor; starting recipe fresh")
				iter = recipeconn.NewConnectorIterator(exec, *src)
			} else {
				iter = ri
			}
		} else {
			iter = recipeconn.NewConnectorIterator(exec, *src)
		}
	} else if resumable, ok := conn.(connectors.ResumableConnector); ok && len(resumeCursor) > 0 {
		iter = resumable.CrawlResume(ctx, *src, &connectors.CheckpointState{
			Cursor: resumeCursor, PageIdx: resumePageIdx, LastURL: resumeLastURL,
		})
	} else {
		iter = conn.Crawl(ctx, *src)
	}

	var (
		jobsFound    int
		jobsEmitted  int
		jobsRejected int
		lastCursor   string
		iterErr      error
		status       = http.StatusOK

		// Run progress, tracked from the iterator's checkpoint each page and
		// flushed to crawl_runs at slice end (Yield/Complete).
		runCursor  = resumeCursor
		runPageIdx = resumePageIdx
		runLastURL = resumeLastURL
		pagesSlice int
		bounded    bool
	)
	sliceMaxPages := h.deps.SliceMaxPages
	if sliceMaxPages <= 0 {
		sliceMaxPages = 50
	}
	sliceMaxDur := time.Duration(h.deps.SliceMaxSeconds) * time.Second
	if sliceMaxDur <= 0 {
		sliceMaxDur = 120 * time.Second
	}
	sliceDeadline := time.Now().Add(sliceMaxDur)

	// advance records one processed page: persist the run's cursor + renew the
	// lease (so the watchdog never reclaims a live slice), then report whether
	// the slice budget (K pages OR T seconds) is exhausted. Called once per
	// page on both the frontier and the direct-extract paths.
	advance := func() bool {
		if cpi, ok := iter.(connectors.CheckpointableIterator); ok {
			if cp := cpi.Checkpoint(); cp != nil {
				runCursor, runPageIdx, runLastURL = cp.Cursor, cp.PageIdx, cp.LastURL
				if run != nil && h.deps.RunRepo != nil {
					if err := h.deps.RunRepo.Progress(ctx, run.ID, cp.Cursor, cp.PageIdx, cp.LastURL, leaseTTL); err != nil {
						util.Log(ctx).WithError(err).Warn("crawl.request: run progress persist failed")
					}
				}
			}
		}
		pagesSlice++
		return run != nil && (pagesSlice >= sliceMaxPages || time.Now().After(sliceDeadline))
	}

pages:
	for iter.Next(ctx) {
		status = iter.HTTPStatus()
		pageItems := iter.Items()

		// D2 — frontier branch. When the source has opted into the
		// URL-frontier path (FrontierEnabled=true) AND the handler
		// has a frontier wired, the iterator's items are treated as
		// URL discoveries: enqueue them and short-circuit the
		// per-URL fetch + extract + emit chain. The frontier-worker
		// takes ownership from here under per-host politeness.
		//
		// Sources with FrontierEnabled=false use source-level extraction.
		if src.FrontierEnabled && h.deps.Frontier != nil {
			enqueued, skipped := h.enqueueFrontier(ctx, src, pageItems)
			jobsFound += enqueued + skipped
			jobsEmitted += enqueued
			jobsRejected += skipped
			if cur := iter.Cursor(); cur != nil {
				lastCursor = string(cur)
			}
			// Persist run progress + renew the lease, then honour the slice
			// budget (the frontier path is just as resumable as direct extract).
			if advance() {
				bounded = true
				break
			}
			continue
		}

		// Connectors must emit complete structured records (API fields or
		// schema.org JSON-LD). Incomplete items fail crawlaccept.Verify —
		// there is no LLM stub enrichment path.

		for i := range pageItems {
			extJob := pageItems[i]
			jobsFound++

			// Single accept path: prepare → verify → normalize → pack.
			// Shared with frontier-worker so "what is a storable job"
			// cannot drift. Does not fall back ApplyURL to source BaseURL
			// (that polluted multi-job boards with listing URLs).
			accepted := crawlaccept.Accept(crawlaccept.Input{
				Opp:        extJob,
				Source:     src,
				Kinds:      h.deps.Kinds,
				Normalizer: h.deps.Normalizer,
			})
			if accepted.Rejected != nil {
				jobsRejected++
				kind := accepted.Rejected.Kind
				telemetry.RecordVerifyRejection(kind, accepted.Rejected.Reason)
				if rerr := h.publishAcceptReject(ctx, src.ID, extJob, accepted.Rejected, crawlJob.ID); rerr != nil {
					log.WithError(rerr).Warn("crawl.request: publishRejected failed")
				}
				continue
			}
			eventPayload := *accepted.Accepted
			kind := eventPayload.Kind

			body, mErr := json.Marshal(eventsv1.NewEnvelope(eventsv1.TopicVariantsIngested, eventPayload))
			if mErr != nil {
				iterErr = fmt.Errorf("marshal variant: %w", mErr)
				break pages
			}
			if h.deps.IngestQueue == nil {
				iterErr = errors.New("PostgreSQL ingest queue is not configured")
				break pages
			}
			idempotencyKey := crawlJob.ID + ":" + eventPayload.HardKey
			if iErr := h.deps.IngestQueue.Enqueue(ctx, jobqueue.EnqueueRequest{
				VariantID: eventPayload.VariantID, SourceID: eventPayload.SourceID,
				CrawlRunID: runID(run), CrawlJobID: crawlJob.ID,
				IdempotencyKey: idempotencyKey, Payload: body,
			}); iErr != nil {
				if errors.Is(iErr, jobqueue.ErrCapacity) {
					bounded = true
					break pages
				}
				iterErr = fmt.Errorf("durably enqueue variant: %w", iErr)
				break pages
			}
			jobsEmitted++
			telemetry.RecordOpportunityReady(kind)
		}

		// Capture the connector's cursor on every successful page; the
		// value at loop end is the source's pagination state.
		if cur := iter.Cursor(); cur != nil {
			lastCursor = string(cur)
		}

		// Persist run progress + renew the lease, then stop the slice if its
		// page/time budget is spent (more pages remain → yield + continuation).
		if advance() {
			bounded = true
			break
		}
		if h.ingestBacklogged(ctx) {
			bounded = true
			break
		}
	}

	if e := iter.Err(); e != nil {
		iterErr = e
		log.WithError(e).Warn("crawl.request: iterator failed")
	}

	// Finalize the run. Three outcomes:
	//   - error: persist the last good cursor and release the lease so the
	//     watchdog re-drives after the lease lapses (paced retry); fail the run
	//     once it has burned through the stuck-attempt ceiling.
	//   - bounded: more pages remain — yield and self-re-enqueue a
	//     backpressure-gated continuation.
	//   - exhausted: the iterator reached the end — complete the run, freeing
	//     the source for a fresh pass on its next scheduled tick.
	if run != nil && h.deps.RunRepo != nil {
		switch {
		case iterErr != nil:
			stuckMax := h.deps.RunStuckMaxAttempts
			if stuckMax > 0 && run.Attempt+1 >= stuckMax {
				if e := h.deps.RunRepo.Fail(ctx, run.ID, "iterator_failed", iterErr.Error(), jobsFound, jobsEmitted, jobsRejected); e != nil {
					log.WithError(e).Warn("crawl.request: run Fail failed")
				}
			} else if e := h.deps.RunRepo.Yield(ctx, run.ID, runCursor, runPageIdx, runLastURL, leaseTTL, jobsFound, jobsEmitted, jobsRejected); e != nil {
				log.WithError(e).Warn("crawl.request: run Yield (after error) failed")
			}
		case bounded:
			if e := h.deps.RunRepo.Yield(ctx, run.ID, runCursor, runPageIdx, runLastURL, leaseTTL, jobsFound, jobsEmitted, jobsRejected); e != nil {
				log.WithError(e).Warn("crawl.request: run Yield failed")
			}
			h.emitContinuation(ctx, src, run.ID, req)
		default:
			if e := h.deps.RunRepo.Complete(ctx, run.ID, runCursor, runPageIdx, runLastURL, jobsFound, jobsEmitted, jobsRejected); e != nil {
				log.WithError(e).Warn("crawl.request: run Complete failed")
			} else if h.deps.IngestQueue != nil {
				if e := h.deps.IngestQueue.ReconcileSource(ctx, src.ID, run.StartedAt); e != nil {
					log.WithError(e).Warn("crawl.request: source reconciliation failed")
				}
			}
		}
	}

	completed := eventsv1.CrawlPageCompletedV1{
		RequestID:    req.RequestID,
		SourceID:     src.ID,
		URL:          src.BaseURL,
		HTTPStatus:   status,
		JobsFound:    jobsFound,
		JobsEmitted:  jobsEmitted,
		JobsRejected: jobsRejected,
		Cursor:       lastCursor,
	}
	if iterErr != nil {
		completed.ErrorCode = "iterator_failed"
		completed.ErrorMessage = iterErr.Error()
	}
	h.emitCompleted(ctx, completed)

	if iterErr != nil {
		telemetry.RecordCrawlCompletion("failure")
	} else {
		telemetry.RecordCrawlCompletion("success")
	}

	if h.deps.CrawlRepo != nil {
		finalStatus := domain.CrawlSucceeded
		errorCode := ""
		errorMessage := ""
		if iterErr != nil {
			finalStatus = domain.CrawlFailed
			errorCode = "iterator_failed"
			errorMessage = iterErr.Error()
		}
		if finErr := h.deps.CrawlRepo.Finish(
			ctx, crawlJob.ID, finalStatus,
			jobsFound, jobsEmitted,
			errorCode, errorMessage,
		); finErr != nil {
			log.WithError(finErr).Warn("crawl.request: finish crawl_jobs failed")
		}
	}

	log.WithField("found", jobsFound).
		WithField("emitted", jobsEmitted).
		WithField("rejected", jobsRejected).
		Info("crawl.request: done")

	return nil
}

// emitContinuation self-re-enqueues the next slice of an in-progress run,
// gated by the backpressure admitter. If admission is denied (the pipeline is
// saturated) or the emit fails, we leave the run yielded — the watchdog
// re-drives it once its lease lapses, so the crawl never stalls or drops work.
func (h *CrawlRequestHandler) emitContinuation(ctx context.Context, src *domain.Source, runID string, req eventsv1.CrawlRequestV1) {
	log := util.Log(ctx).WithField("source_id", src.ID).WithField("run_id", runID)
	if h.deps.Admitter != nil {
		if granted, wait := h.deps.Admitter.Admit(ctx, eventsv1.TopicCrawlRequests, 1); granted < 1 {
			telemetry.RecordCrawlDispatch("denied", "continuation")
			log.WithField("retry_after_sec", int(wait/time.Second)).
				Debug("crawl.request: continuation deferred by backpressure; watchdog will re-drive")
			return
		}
	}
	env := eventsv1.NewEnvelope(eventsv1.TopicCrawlRequests, eventsv1.CrawlRequestV1{
		RequestID:      xid.New().String(),
		SourceID:       src.ID,
		ScheduledAt:    req.ScheduledAt,
		Mode:           "auto",
		Attempt:        req.Attempt + 1,
		RunID:          runID,
		IsContinuation: true,
	})
	if err := h.deps.Svc.EventsManager().Emit(ctx, eventsv1.TopicCrawlRequests, env); err != nil {
		log.WithError(err).Warn("crawl.request: continuation emit failed; watchdog will re-drive")
		return
	}
	telemetry.RecordCrawlDispatch("emitted", "continuation")
}

func (h *CrawlRequestHandler) ingestBacklogged(ctx context.Context) bool {
	if h.deps.IngestQueue == nil {
		return true
	}
	stats, err := h.deps.IngestQueue.Stats(ctx)
	if err != nil {
		util.Log(ctx).WithError(err).Warn("crawl.request: ingest backlog unavailable; pausing crawl")
		return true
	}
	return (h.deps.IngestMaxPending > 0 && stats.Pending >= h.deps.IngestMaxPending) ||
		(h.deps.IngestMaxOldestAge > 0 && stats.OldestAge >= h.deps.IngestMaxOldestAge)
}

func runID(run *domain.CrawlRun) string {
	if run == nil {
		return ""
	}
	return run.ID
}

// emitCompleted publishes a CrawlPageCompletedV1 envelope. Best-
// effort — if emission fails (rare; Frame transport down) we log and
// return. Next crawl picks up the source again via scheduler tick.
func (h *CrawlRequestHandler) emitCompleted(ctx context.Context, payload eventsv1.CrawlPageCompletedV1) {
	env := eventsv1.NewEnvelope(eventsv1.TopicCrawlPageCompleted, payload)
	if err := h.deps.Svc.EventsManager().Emit(ctx, eventsv1.TopicCrawlPageCompleted, env); err != nil {
		util.Log(ctx).WithError(err).WithField("source_id", payload.SourceID).
			Warn("crawl.request: emit page-completed failed")
	}
}

// publishAcceptReject records an immutable rejection audit event from the
// shared crawlaccept path.
func (h *CrawlRequestHandler) publishAcceptReject(
	ctx context.Context, sourceID string,
	opp domain.ExternalOpportunity, rej *crawlaccept.Reject,
	crawlJobID string,
) error {
	reasons := append([]string(nil), rej.Missing...)
	if rej.Detail != "" {
		reasons = append(reasons, rej.Detail)
	}
	if len(reasons) == 0 {
		reasons = []string{rej.Reason}
	}
	row := eventsv1.VariantRejectedV1{
		VariantID:  xid.New().String(),
		SourceID:   sourceID,
		Kind:       rej.Kind,
		Title:      opp.Title,
		Reasons:    reasons,
		CrawlJobID: crawlJobID,
		RejectedAt: time.Now().UTC(),
	}
	if h.deps.IngestQueue == nil {
		return errors.New("PostgreSQL ingest queue is not configured")
	}
	return h.deps.IngestQueue.RecordRejected(ctx, row.VariantID, sourceID, row)
}

// enqueueFrontier walks the iterator's items and enqueues each
// SourceURL into the URL frontier. The frontier-worker fetches each URL
// and extracts structured JobPosting JSON-LD under per-host politeness.
//
// Returns (enqueued, skipped) so the caller can fold them into
// the page-completed counters (jobs_emitted + jobs_rejected).
// Skipped covers items with no SourceURL (the discovery output
// is meaningless without a URL to fetch) and any per-row Enqueue
// errors. The whole batch never fails — a transient Postgres
// error is logged and the page moves on; the next crawl tick
// will re-enqueue. Duplicates are silently deduped by the
// canonical_url_hash unique index in pkg/frontier.
func (h *CrawlRequestHandler) enqueueFrontier(
	ctx context.Context,
	src *domain.Source,
	items []domain.ExternalOpportunity,
) (enqueued, skipped int) {
	if h.deps.Frontier == nil || len(items) == 0 {
		return 0, len(items)
	}
	log := util.Log(ctx).WithField("source_id", src.ID)

	batch := make([]frontier.URL, 0, len(items))
	for i := range items {
		u := items[i].SourceURL
		if u == "" {
			u = items[i].ApplyURL
		}
		if u == "" {
			skipped++
			continue
		}
		// Canonicalize before enqueue so the stored canonical_url and
		// its dedup hash are the normalized form. Enqueue canonicalizes
		// again defensively; doing it here keeps host derivation consistent.
		u = frontier.CanonicalizeURL(u)
		host := frontier.HostOf(u)
		if host == "" {
			skipped++
			continue
		}
		// Priority: source.score is in [0..1] already; treat 0.7
		// weight on source-level signal + 0.3 weight on a
		// neutral URL signal (no per-URL signals available yet).
		// The frontier-worker re-reads the URL on Dequeue so the
		// snapshot priority isn't load-bearing — it just steers
		// fairness across hosts.
		priority := src.Score*0.7 + 0.5*0.3
		batch = append(batch, frontier.URL{
			CanonicalURL: u,
			Host:         host,
			SourceID:     src.ID,
			Priority:     priority,
		})
	}
	if len(batch) == 0 {
		return 0, skipped
	}
	results, err := h.deps.Frontier.Enqueue(ctx, batch)
	if err != nil {
		log.WithError(err).Warn("crawl.request: frontier enqueue had per-row errors")
	}
	enqueued = len(results)
	skipped += len(batch) - len(results)
	log.WithField("enqueued", enqueued).
		WithField("skipped", skipped).
		Info("crawl.request: enqueued URLs into frontier")
	return enqueued, skipped
}
