package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"
	"github.com/rs/xid"

	"github.com/stawi-opportunities/opportunities/pkg/archive"
	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/recipeconn"
	"github.com/stawi-opportunities/opportunities/pkg/content"
	"github.com/stawi-opportunities/opportunities/pkg/crawlinbox"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/frontier"
	"github.com/stawi-opportunities/opportunities/pkg/normalize"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/recipe"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
	"github.com/stawi-opportunities/opportunities/pkg/telemetry"
	"github.com/stawi-opportunities/opportunities/pkg/variantstate"
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
	// IngestedQueue is the Frame Queue Name the crawler publishes
	// VariantIngestedV1 to — the head of the opportunity pipeline chain
	// (consumed by the worker's normalize stage).
	IngestedQueue string
	// Inbox, when non-nil, buffers crawled variants into the crawl_inbox
	// Postgres table instead of publishing straight to IngestedQueue. A
	// rate-limited pump (see cmd/main.go) drains the inbox onto pl_ingested,
	// so a crawl burst lands in the DB rather than slamming JetStream. nil →
	// legacy direct-publish.
	Inbox   *crawlinbox.Store
	Sources SourceGetter
	// StatusSetter (optional) pauses a source the handler can't crawl — no
	// connector and no recipe (e.g. an individual ATS board whose connector was
	// migrated to an aggregate recipe). nil disables auto-pause.
	StatusSetter SourceStatusSetter
	Registry     *connectors.Registry
	Kinds        *opportunity.Registry // opportunity-kind registry; required by Verify
	Archive      archive.Archive
	Extractor    *extraction.Extractor // nil → skip AI enrichment
	Normalizer   *normalize.Normalizer // nil → fall back to raw ExternalToVariant (no geocoder)
	// PageFetcher fetches per-URL HTML so URL-only iterator stubs
	// (sitemap, universal AI link discovery) can be enriched with
	// LLM-extracted title/description/issuing_entity BEFORE Verify.
	// nil → stubs flow through unchanged and are rejected by Verify.
	PageFetcher *httpx.Client
	// EnrichConcurrency bounds how many URL-stub fetch+extract calls
	// run concurrently per page. Defaults to 4. Sized so multiple
	// crawler pods × concurrent stubs stay below the configured llama
	// slot budget; higher values overwhelm the inference fleet and
	// trigger Frame ants-pool exhaustion downstream.
	EnrichConcurrency int
	// DiscoverSample is the probability [0..1] that a given iterator
	// page triggers an additional DiscoverSites call. 0.0 = disabled
	// (unit-test default). In production, set ~0.05 so roughly one in
	// twenty crawled pages attempts site discovery. Multi-page connectors
	// get correspondingly more rolls per source crawl.
	DiscoverSample float64
	// Rand is optional; nil uses a package-local deterministic source
	// seeded in init(). Tests can inject a fixed seed to make sampling
	// predictable.
	Rand *rand.Rand
	// VariantStore writes to the pipeline_variants Postgres ledger so
	// ops can answer "where is variant X?" without scanning NATS. nil
	// disables ledger writes (the worker's defensive Upsert backstops
	// missing rows). Soft-fail throughout: a Postgres outage degrades
	// observability but does not stall the chain.
	VariantStore *variantstate.Store
	// CrawlRepo writes the crawl_jobs + raw_payloads audit ledger.
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

	// Frontier wires the D2 URL-frontier path. When non-nil AND the
	// source has FrontierEnabled=true, the crawl handler enqueues
	// discovered URLs into the frontier instead of running the
	// extract+emit pipeline in-line. The frontier-worker then handles
	// the per-URL fetch + extract under per-host politeness.
	//
	// nil disables the path entirely — every source falls back to
	// the legacy direct-extract behaviour regardless of the flag.
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

// CrawlRequestHandler consumes jobs.crawl.requests.v1, runs the
// connector iterator for the source, archives raw HTML, optionally
// extracts job fields via AI, and emits:
//
//   - one jobs.variants.ingested.v1 per accepted job
//   - optionally one sources.discovered.v1 per DiscoverSites hit
//   - exactly one crawl.page.completed.v1 summary per request
type CrawlRequestHandler struct {
	deps CrawlRequestDeps
}

// NewCrawlRequestHandler wires the handler. Deps are captured by value
// because nothing in the struct is mutable between calls.
func NewCrawlRequestHandler(deps CrawlRequestDeps) *CrawlRequestHandler {
	if deps.Rand == nil {
		deps.Rand = rand.New(rand.NewSource(time.Now().UnixNano())) //nolint:gosec
	}
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

	// Reparse short-circuit: when RawPayloadID is set, the request was
	// issued by the /admin/raw_payloads/{id}/reparse (or
	// /admin/sources/{id}/reparse) endpoint. Skip the connector
	// iterator entirely and re-run extraction on the stored HTML.
	if req.RawPayloadID != "" {
		return h.reparse(ctx, req)
	}

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

	// Resolve the crawl path: a source with an active extraction recipe runs
	// via the deterministic recipe Executor; otherwise its registered
	// connector. Both drive the same crawl_runs state machine below.
	var (
		rec        *recipe.Recipe
		usedRecipe bool
		conn       connectors.Connector
	)
	if h.deps.RecipeEnabled && h.deps.RecipeRepo != nil {
		r, rErr := h.deps.RecipeRepo.Active(ctx, src.ID)
		if rErr != nil {
			log.WithError(rErr).Warn("crawl.request: recipe lookup failed; using connector")
		} else if useRecipePath(h.deps.RecipeEnabled, r) {
			rec = r
			usedRecipe = true
		}
	}
	if !usedRecipe {
		c, ok := h.deps.Registry.Get(src.Type)
		if !ok {
			// No connector AND no recipe → this source can't be crawled. The
			// common case is an individual ATS board (greenhouse/lever) whose
			// connector was migrated to an aggregate recipe; left active it
			// burns a dispatch on every tick. Auto-pause it so it self-heals
			// (an operator or the aggregate owns its coverage), then ack.
			if h.deps.StatusSetter != nil {
				if perr := h.deps.StatusSetter.SetStatus(ctx, src.ID, domain.SourcePaused); perr != nil {
					log.WithError(perr).Warn("crawl.request: auto-pause of connector-less source failed")
				} else {
					log.WithField("source_type", src.Type).
						Warn("crawl.request: no connector — paused source to stop futile dispatch")
				}
			} else {
				log.WithField("source_type", src.Type).Warn("crawl.request: no connector")
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

	for iter.Next(ctx) {
		status = iter.HTTPStatus()
		pageArchiveRef := resolveArchiveRef(ctx, h.deps.Archive, iter.Content())

		// Audit-ledger: row per page. resolveArchiveRef already wrote the R2
		// blob via archive.PutRaw (content-addressed by sha256); here we
		// just record the metadata + queue-state row pointing at it.
		var pageRawPayloadID string
		if h.deps.CrawlRepo != nil && iter.Content() != nil && len(iter.Content().RawHTML) > 0 {
			rawBody := []byte(iter.Content().RawHTML)
			pageContentHash := sha256Hex(rawBody)
			rp := &domain.RawPayload{
				CrawlJobID:  crawlJob.ID,
				SourceID:    src.ID,
				SourceURL:   src.BaseURL,
				StorageURI:  pageArchiveRef,
				ContentHash: pageContentHash,
				SizeBytes:   int64(len(rawBody)),
				FetchedAt:   time.Now().UTC(),
				HTTPStatus:  iter.HTTPStatus(),
				Status:      domain.RawPayloadStatusPending,
			}
			if rawErr := h.deps.CrawlRepo.SaveRawPayload(ctx, rp); rawErr != nil {
				log.WithError(rawErr).Warn("crawl.request: save raw_payload failed")
			} else {
				pageRawPayloadID = rp.ID
			}
		}

		pageItems := iter.Items()

		// D2 — frontier branch. When the source has opted into the
		// URL-frontier path (FrontierEnabled=true) AND the handler
		// has a frontier wired, the iterator's items are treated as
		// URL discoveries: enqueue them and short-circuit the
		// per-URL fetch + extract + emit chain. The frontier-worker
		// takes ownership from here under per-host politeness.
		//
		// Sources with FrontierEnabled=false (every source by
		// default) flow through the legacy direct-extract path
		// below — zero change in behaviour.
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

		// Enrich URL-only stubs (sitemap + universal AI link
		// discovery) by fetching each detail page and running the
		// LLM extractor in parallel. Mutates pageItems in place.
		// Connectors that already produce complete records (greenhouse,
		// themuse, …) skip this step because their Title is non-empty.
		h.enrichStubs(ctx, pageItems, src)

		for i := range pageItems {
			extJob := pageItems[i]
			jobsFound++

			// Apply-URL fallback chain first — normalize and Verify both see the
			// resolved URL.
			ensureApplyURL(&extJob, extJob.SourceURL)
			if extJob.ApplyURL == "" {
				ensureApplyURL(&extJob, src.BaseURL)
			}

			// Resolve kind: prefer the connector-tagged kind on the
			// ExternalOpportunity, falling back to the source's first
			// declared Kind so single-kind connectors keep working when a
			// connector forgets to tag.
			kind := extJob.Kind
			if kind == "" && len(src.Kinds) > 0 {
				kind = src.Kinds[0]
				extJob.Kind = kind
			}

			// Anchor-country fallback chain. Every source carries a
			// declared country (geolocated at registration). When the
			// LLM extractor fails to populate AnchorLocation.Country —
			// which is most pages in practice; LLMs miss it on listings
			// that don't mention the country explicitly — fall back to
			// the source's country. Without this, every variant fails
			// opportunity.Verify's "anchor_country" check, dead-letters
			// to variants.rejected.v1, and the canonical chain never
			// produces a single canonicals.upserted.v1 event —
			// observable in production as the materializer acking
			// thousands of variants.rejected events with zero rows
			// ever landing in idx_opportunities_rt.
			if src.Country != "" {
				if extJob.AnchorLocation == nil {
					extJob.AnchorLocation = &domain.Location{Country: src.Country}
				} else if extJob.AnchorLocation.Country == "" {
					extJob.AnchorLocation.Country = src.Country
				}
			}

			// Source contract + kind contract gate. Replaces the old
			// pkg/quality/gate.Check. Rejected records dead-letter to
			// opportunities.variants.rejected.v1 (and the matching Iceberg
			// table via the writer subscription).
			if h.deps.Kinds != nil {
				if res := opportunity.Verify(&extJob, src, h.deps.Kinds); !res.OK {
					jobsRejected++
					reason := rejectionReason(res)
					telemetry.RecordVerifyRejection(kind, reason)
					if rerr := h.publishRejected(ctx, src.ID, kind, extJob, res, crawlJob.ID, pageRawPayloadID); rerr != nil {
						log.WithError(rerr).Warn("crawl.request: publishRejected failed")
					}
					continue
				}
			}

			// Convert to a VariantIngested payload via the normalize
			// helper. Hard-key, stage, and mapping live there. When a
			// Normalizer is wired (production), it runs the bundled
			// gazetteer enrich pass first so AnchorLocation gets
			// Lat/Lon/Region filled in for recognised cities.
			now := time.Now().UTC()
			var variant normalize.JobVariant
			if h.deps.Normalizer != nil {
				variant = h.deps.Normalizer.Normalize(
					&extJob, src.ID, src.Country, string(src.Type), src.Language, now,
				)
			} else {
				variant = normalize.ExternalToVariant(
					extJob, src.ID, src.Country, string(src.Type), src.Language, now,
				)
			}

			if kind == "" {
				// Belt-and-braces — Verify above would have rejected an
				// empty kind, but keep the legacy fallback so a missing
				// Kinds registry never empties the kind column.
				kind = "job"
			}

			// Pack the kind-specific fields into Attributes so the new
			// polymorphic VariantIngestedV1 carries them through. The
			// universal envelope fields (title, currency, anchor) ride
			// at the top level for partition pruning.
			//
			// Start from the connector-/extractor-supplied Attributes so
			// kind-specific keys (e.g. field_of_study, degree_level for
			// scholarships) survive the normalize step. The job-shaped
			// overlays below are harmless for non-job kinds — they ride
			// in as empty strings — but stay required for jobs.
			attrs := maps.Clone(extJob.Attributes)
			if attrs == nil {
				attrs = map[string]any{}
			}
			attrs["description"] = variant.Description
			attrs["apply_url"] = variant.ApplyURL
			attrs["language"] = variant.Language
			attrs["remote_type"] = variant.RemoteType
			attrs["employment_type"] = variant.EmploymentType
			attrs["location_text"] = variant.LocationText
			attrs["content_hash"] = variant.ContentHash
			attrs["raw_archive_ref"] = pageArchiveRef
			if variant.PostedAt != nil {
				attrs["posted_at"] = variant.PostedAt.Format(time.RFC3339)
			}

			eventPayload := eventsv1.VariantIngestedV1{
				VariantID:     xid.New().String(),
				SourceID:      src.ID,
				ExternalID:    variant.ExternalJobID,
				HardKey:       variant.HardKey,
				Kind:          kind,
				Stage:         string(domain.StageRaw),
				Title:         variant.Title,
				IssuingEntity: variant.Company,
				AnchorCountry: variant.Country,
				Remote:        variant.RemoteType == "remote",
				Currency:      variant.Currency,
				AmountMin:     variant.SalaryMin,
				AmountMax:     variant.SalaryMax,
				Attributes:    attrs,
				ScrapedAt:     now,
			}

			// Write the ledger row BEFORE the NATS emit so the worker's
			// AdvanceStage(ingested → normalized) always finds an existing
			// row. The normalize handler also Upserts defensively for
			// the rare case where Postgres replication lag puts the row
			// behind the NATS delivery, but the canonical path is:
			// crawler writes → crawler emits → worker reads.
			rawIDPtr := stringPtrOrNil(pageRawPayloadID)
			jobIDPtr := stringPtrOrNil(crawlJob.ID)
			_ = h.deps.VariantStore.Upsert(ctx, variantstate.Variant{
				VariantID:    eventPayload.VariantID,
				SourceID:     eventPayload.SourceID,
				HardKey:      eventPayload.HardKey,
				Kind:         eventPayload.Kind,
				CurrentStage: variantstate.StageIngested,
				RawPayloadID: rawIDPtr,
				CrawlJobID:   jobIDPtr,
			})

			body, mErr := json.Marshal(eventsv1.NewEnvelope(eventsv1.TopicVariantsIngested, eventPayload))
			if mErr != nil {
				log.WithError(mErr).Warn("crawl.request: marshal variant failed")
				continue
			}
			if h.deps.Inbox != nil {
				if iErr := h.deps.Inbox.Insert(ctx, eventPayload.VariantID, eventPayload.SourceID, body); iErr != nil {
					log.WithError(iErr).Warn("crawl.request: inbox insert failed")
					telemetry.RecordCrawlSilentLoss("inbox_insert_drop")
					continue
				}
			} else if pErr := h.deps.Svc.QueueManager().Publish(ctx, h.deps.IngestedQueue, body, nil); pErr != nil {
				log.WithError(pErr).Warn("crawl.request: publish variant failed")
				telemetry.RecordCrawlSilentLoss("variant_publish_drop")
				continue
			}
			jobsEmitted++
			telemetry.RecordOpportunityReady(kind)
		}

		// Capture the connector's cursor on every successful page; the
		// value at loop end is the source's pagination state.
		if cur := iter.Cursor(); cur != nil {
			lastCursor = string(cur)
		}

		// Opportunistic DiscoverSites — sampled so we don't triple the
		// AI bill. Operates on the last page's raw HTML (iter.Content
		// is already parsed; use RawPayload for the unprocessed bytes).
		if h.deps.Extractor != nil && h.deps.DiscoverSample > 0 &&
			h.deps.Rand.Float64() < h.deps.DiscoverSample {
			h.sampleDiscoverSites(ctx, src, iter.RawPayload())
		}

		// Persist run progress + renew the lease, then stop the slice if its
		// page/time budget is spent (more pages remain → yield + continuation).
		if advance() {
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

// sampleDiscoverSites is best-effort. DiscoverSites cost is not on the
// user hot path and we never want a discovery failure to fail the
// surrounding crawl — swallow errors.
func (h *CrawlRequestHandler) sampleDiscoverSites(ctx context.Context, src *domain.Source, raw []byte) {
	if h.deps.Extractor == nil || len(raw) == 0 {
		return
	}
	sites, err := h.deps.Extractor.DiscoverSites(ctx, string(raw), src.BaseURL)
	if err != nil {
		util.Log(ctx).WithError(err).Debug("crawl.request: DiscoverSites failed (sampled)")
		return
	}
	evtMgr := h.deps.Svc.EventsManager()
	for _, site := range sites {
		env := eventsv1.NewEnvelope(eventsv1.TopicSourcesDiscovered, eventsv1.SourceDiscoveredV1{
			DiscoveredURL: site.URL,
			Name:          site.Name,
			Country:       site.Country,
			Type:          site.Type,
			SourceID:      src.ID,
		})
		_ = evtMgr.Emit(ctx, eventsv1.TopicSourcesDiscovered, env)
	}
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

// resolveArchiveRef archives the page's raw HTML (if any) and returns
// the R2 object key. Called once per iterator page; idempotent via
// HasRaw + content-addressed PutRaw. Returns "" on missing content or
// archive error (best-effort — missing ref is recoverable, dropping
// the variant is not).
func resolveArchiveRef(ctx context.Context, arch archive.Archive, page *content.Extracted) string {
	if page == nil || len(page.RawHTML) == 0 {
		return ""
	}
	body := []byte(page.RawHTML)
	hash := sha256Hex(body)
	if has, hasErr := arch.HasRaw(ctx, hash); hasErr == nil && !has {
		if putHash, _, putErr := arch.PutRaw(ctx, body); putErr == nil {
			return archive.RawKey(putHash)
		}
		// Variants emitted without a ref can never be reparsed — count it
		// so a dead archive shows up as a trend, not a debug-log mystery.
		telemetry.RecordCrawlSilentLoss("archive_ref_missing")
		return ""
	}
	return archive.RawKey(hash)
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// stringPtrOrNil returns a pointer to s, or nil when s is empty. Used
// to keep RawPayloadID / CrawlJobID columns NULL on the variant ledger
// row when the audit-write soft-failed (best-effort) or the deps
// haven't wired the CrawlRepo (test paths).
func stringPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// publishRejected emits VariantRejectedV1 for a record that failed
// opportunity.Verify. The writer subscribes to the matching topic and
// appends a row to opportunities.variants_rejected so the rejection is
// durable and operator-inspectable.
func (h *CrawlRequestHandler) publishRejected(
	ctx context.Context, sourceID, kind string,
	opp domain.ExternalOpportunity, res opportunity.VerifyResult,
	crawlJobID, rawPayloadID string,
) error {
	reasons := append([]string(nil), res.Missing...)
	if res.Mismatch != "" {
		reasons = append(reasons, res.Mismatch)
	}
	if len(reasons) == 0 {
		reasons = []string{"verify_failed"}
	}
	rej := eventsv1.VariantRejectedV1{
		VariantID:    xid.New().String(),
		SourceID:     sourceID,
		Kind:         kind,
		Title:        opp.Title,
		Reasons:      reasons,
		RawPayloadID: rawPayloadID,
		CrawlJobID:   crawlJobID,
		RejectedAt:   time.Now().UTC(),
	}
	// Ledger row at terminal stage 'rejected' — never enters the
	// normal chain so AdvanceStage wouldn't find a prior row. We
	// write directly with CurrentStage=rejected; HardKey carries the
	// computed dedup key when available so ops can correlate
	// rejections with later accepted variants of the same logical job.
	hk := opp.ExternalID
	if hk == "" {
		hk = opp.Title
	}
	_ = h.deps.VariantStore.Upsert(ctx, variantstate.Variant{
		VariantID:    rej.VariantID,
		SourceID:     sourceID,
		HardKey:      hk,
		Kind:         kind,
		CurrentStage: variantstate.StageRejected,
	})
	env := eventsv1.NewEnvelope(eventsv1.TopicVariantsRejected, rej)
	return h.deps.Svc.EventsManager().Emit(ctx, eventsv1.TopicVariantsRejected, env)
}

// rejectionReason categorises a VerifyResult into a low-cardinality
// metric attribute. Mismatch wins over missing because a kind-mismatch
// rejection is a source-config bug, while a missing field is a
// connector or extractor bug.
func rejectionReason(r opportunity.VerifyResult) string {
	if r.Mismatch != "" {
		return "mismatch"
	}
	if len(r.Missing) > 0 {
		return "missing_" + r.Missing[0]
	}
	return "unknown"
}

// ensureApplyURL sets opp.ApplyURL to fallbackURL if the field is
// currently empty. Replaces pkg/quality.EnsureApplyURL — the only
// remaining surface that helper provided once Verify took over the
// content gate.
func ensureApplyURL(opp *domain.ExternalOpportunity, fallbackURL string) {
	if strings.TrimSpace(opp.ApplyURL) == "" && fallbackURL != "" {
		opp.ApplyURL = fallbackURL
	}
}

// enrichStubs fetches per-URL HTML and runs the LLM extractor on
// any URL-only stub the connector yielded (Title empty but ApplyURL
// set). This is the second AI hop in the crawl pipeline: the first
// hop discovers URLs (sitemap-XML or universal.DiscoverLinks); this
// hop turns each URL into a fully-populated ExternalOpportunity.
// Mutates items in place.
//
// Connectors that already emit complete records (greenhouse, themuse,
// arbeitnow, …) skip enrichment because their Title is non-empty.
//
// Bounded concurrency keeps the llama fleet within its served-slot
// budget; values above ~8 saturate inference and propagate timeouts
// into the worker pool downstream.
func (h *CrawlRequestHandler) enrichStubs(ctx context.Context, items []domain.ExternalOpportunity, src *domain.Source) {
	if h.deps.Extractor == nil || h.deps.PageFetcher == nil {
		return
	}
	// EnrichConcurrency==0 disables enrichment entirely — used as a
	// load-shedding lever when the shared inference fleet is saturated
	// and we'd rather let URL-only stubs dead-letter than backpressure
	// the LLM-dependent worker pipeline.
	if h.deps.EnrichConcurrency == 0 {
		return
	}

	stubIdx := make([]int, 0, len(items))
	for i := range items {
		if strings.TrimSpace(items[i].Title) == "" && strings.TrimSpace(items[i].ApplyURL) != "" {
			stubIdx = append(stubIdx, i)
		}
	}
	if len(stubIdx) == 0 {
		return
	}

	conc := h.deps.EnrichConcurrency
	if conc < 0 {
		conc = 4
	}
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup

	for _, i := range stubIdx {
		sem <- struct{}{}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()
			// Per-stub deadline so one slow detail page (fetch +
			// inference) can't pin wg.Wait() — and the whole crawl
			// handler — for minutes. 6m matches the inference ceiling.
			stubCtx, cancel := context.WithTimeout(ctx, 6*time.Minute)
			defer cancel()
			h.enrichOne(stubCtx, &items[idx], src)
		}(i)
	}
	wg.Wait()
}

// enrichOne fetches one detail page and merges LLM-extracted fields
// into the stub. Best-effort — fetch errors, non-200, parse failures,
// or empty extractions all leave the stub untouched (Verify rejects
// them with the usual missing-field reasons). Logging stays at debug
// to avoid flooding the crawler when whole sitemaps go 404.
func (h *CrawlRequestHandler) enrichOne(ctx context.Context, opp *domain.ExternalOpportunity, src *domain.Source) {
	raw, status, err := h.deps.PageFetcher.Get(ctx, opp.ApplyURL, nil)
	if err != nil || status != 200 || len(raw) == 0 {
		if err != nil {
			util.Log(ctx).WithError(err).WithField("url", opp.ApplyURL).Debug("enrich: fetch failed")
		}
		return
	}

	rawHTML := string(raw)
	// Company logo: capture the detail page's og:image (best-effort, only when
	// structured data hasn't already supplied one). Flows to the company record
	// via attributes.company_logo.
	if logo := content.OGImage(rawHTML); logo != "" {
		if opp.Attributes == nil {
			opp.Attributes = map[string]any{}
		}
		if _, has := opp.Attributes["company_logo"]; !has {
			opp.Attributes["company_logo"] = logo
		}
	}

	body := rawHTML
	if ext, _ := content.ExtractFromHTML(body); ext != nil && ext.Markdown != "" {
		body = ext.Markdown
	}

	extracted, err := h.deps.Extractor.Extract(ctx, body, src.Kinds, src.ExtractionPromptExtension)
	if err != nil || extracted == nil {
		if err != nil {
			util.Log(ctx).WithError(err).WithField("url", opp.ApplyURL).Debug("enrich: LLM extract failed")
		}
		return
	}

	mergeStubFields(opp, extracted)
}

// mergeStubFields copies fields from the LLM-extracted record into
// the stub, preserving stub-supplied identity (ExternalID, ApplyURL,
// Kind) and only filling empty fields. Attributes merge key-by-key
// without overwriting connector-set values.
func mergeStubFields(dst, src *domain.ExternalOpportunity) {
	if dst.Title == "" {
		dst.Title = src.Title
	}
	if dst.Description == "" {
		dst.Description = src.Description
	}
	if dst.IssuingEntity == "" {
		dst.IssuingEntity = src.IssuingEntity
	}
	if dst.LocationText == "" {
		dst.LocationText = src.LocationText
	}
	if dst.AnchorLocation == nil && src.AnchorLocation != nil {
		dst.AnchorLocation = src.AnchorLocation
	}
	if dst.Kind == "" {
		dst.Kind = src.Kind
	}
	if dst.Deadline == nil {
		dst.Deadline = src.Deadline
	}
	if dst.Currency == "" {
		dst.Currency = src.Currency
	}
	if dst.AmountMin == 0 {
		dst.AmountMin = src.AmountMin
	}
	if dst.AmountMax == 0 {
		dst.AmountMax = src.AmountMax
	}
	if len(src.Attributes) > 0 {
		if dst.Attributes == nil {
			dst.Attributes = map[string]any{}
		}
		for k, v := range src.Attributes {
			if _, has := dst.Attributes[k]; !has {
				dst.Attributes[k] = v
			}
		}
	}
}

// reparse re-runs extraction on a previously-fetched HTML page,
// bypassing the connector iterator. Used by the
// /admin/raw_payloads/{id}/reparse and /admin/sources/{id}/reparse
// endpoints after operators edit extraction prompts or kind specs.
//
// Always best-effort: storage misses, expired retention, source-not-
// found, extraction errors, and verify rejections all log + return nil
// rather than triggering Frame redelivery. The audit columns
// (raw_payloads.reparse_count + last_reparsed_at) are bumped on every
// invocation regardless of outcome so operators can see how often a
// given payload has been re-tried.
func (h *CrawlRequestHandler) reparse(ctx context.Context, req eventsv1.CrawlRequestV1) error {
	log := util.Log(ctx).
		WithField("raw_payload_id", req.RawPayloadID).
		WithField("source_id", req.SourceID)
	if h.deps.CrawlRepo == nil {
		return errors.New("reparse: CrawlRepo unwired")
	}

	rp, err := h.deps.CrawlRepo.GetRawPayload(ctx, req.RawPayloadID)
	if err != nil {
		return fmt.Errorf("reparse: GetRawPayload: %w", err)
	}
	if rp == nil {
		log.Warn("reparse: raw_payload not found (retention expired or wrong id)")
		return nil // not a NATS-level failure
	}

	src, err := h.deps.Sources.GetByID(ctx, rp.SourceID)
	if err != nil {
		return fmt.Errorf("reparse: source lookup: %w", err)
	}
	if src == nil {
		log.Warn("reparse: source not found")
		_ = h.deps.CrawlRepo.IncrementReparseCount(ctx, rp.ID)
		return nil
	}
	if len(src.Kinds) == 0 {
		src.Kinds = []string{"job"}
	}

	body, err := h.deps.Archive.GetRaw(ctx, rp.ContentHash)
	if err != nil {
		log.WithError(err).Warn("reparse: archive.GetRaw failed")
		_ = h.deps.CrawlRepo.IncrementReparseCount(ctx, rp.ID)
		return nil
	}

	bodyStr := string(body)
	if ext, _ := content.ExtractFromHTML(bodyStr); ext != nil && ext.Markdown != "" {
		bodyStr = ext.Markdown
	}

	if h.deps.Extractor == nil {
		log.Warn("reparse: Extractor unwired; nothing to re-run")
		_ = h.deps.CrawlRepo.IncrementReparseCount(ctx, rp.ID)
		return nil
	}
	extracted, err := h.deps.Extractor.Extract(ctx, bodyStr, src.Kinds, src.ExtractionPromptExtension)
	if err != nil {
		log.WithError(err).Warn("reparse: extraction failed")
		_ = h.deps.CrawlRepo.IncrementReparseCount(ctx, rp.ID)
		return nil
	}
	if extracted == nil {
		log.Info("reparse: extractor returned nothing")
		_ = h.deps.CrawlRepo.IncrementReparseCount(ctx, rp.ID)
		return nil
	}

	// Re-attach source identity + URL hints — Extract returns a fresh
	// record without the SourceID/SourceURL fields populated.
	extJob := *extracted
	extJob.SourceID = src.ID
	if extJob.SourceURL == "" {
		extJob.SourceURL = rp.SourceURL
	}
	ensureApplyURL(&extJob, rp.SourceURL)
	ensureApplyURL(&extJob, src.BaseURL)

	kind := extJob.Kind
	if kind == "" && len(src.Kinds) > 0 {
		kind = src.Kinds[0]
		extJob.Kind = kind
	}
	if src.Country != "" {
		if extJob.AnchorLocation == nil {
			extJob.AnchorLocation = &domain.Location{Country: src.Country}
		} else if extJob.AnchorLocation.Country == "" {
			extJob.AnchorLocation.Country = src.Country
		}
	}

	// Verify — if still rejected, emit the rejection event with the
	// raw_payload back-reference; the operator can iterate.
	if h.deps.Kinds != nil {
		if res := opportunity.Verify(&extJob, src, h.deps.Kinds); !res.OK {
			log.WithField("reasons", res.Missing).Info("reparse: still rejected after re-extraction")
			_ = h.publishRejected(ctx, src.ID, kind, extJob, res, "", rp.ID)
			_ = h.deps.CrawlRepo.IncrementReparseCount(ctx, rp.ID)
			return nil
		}
	}

	// Emit a fresh variants.ingested.v1 — the rest of the pipeline
	// (normalize → validate → cluster → canonical → publish) handles
	// it identically to a fresh crawl.
	now := time.Now().UTC()
	var variant normalize.JobVariant
	if h.deps.Normalizer != nil {
		variant = h.deps.Normalizer.Normalize(&extJob, src.ID, src.Country, string(src.Type), src.Language, now)
	} else {
		variant = normalize.ExternalToVariant(extJob, src.ID, src.Country, string(src.Type), src.Language, now)
	}
	if kind == "" {
		kind = "job"
	}
	attrs := maps.Clone(extJob.Attributes)
	if attrs == nil {
		attrs = map[string]any{}
	}
	attrs["description"] = variant.Description
	attrs["apply_url"] = variant.ApplyURL
	attrs["language"] = variant.Language
	attrs["remote_type"] = variant.RemoteType
	attrs["employment_type"] = variant.EmploymentType
	attrs["location_text"] = variant.LocationText
	attrs["content_hash"] = variant.ContentHash
	attrs["raw_archive_ref"] = rp.StorageURI
	attrs["reparsed_from"] = rp.ID
	if variant.PostedAt != nil {
		attrs["posted_at"] = variant.PostedAt.Format(time.RFC3339)
	}

	eventPayload := eventsv1.VariantIngestedV1{
		VariantID:     xid.New().String(),
		SourceID:      src.ID,
		ExternalID:    variant.ExternalJobID,
		HardKey:       variant.HardKey,
		Kind:          kind,
		Stage:         string(domain.StageRaw),
		Title:         variant.Title,
		IssuingEntity: variant.Company,
		AnchorCountry: variant.Country,
		Remote:        variant.RemoteType == "remote",
		Currency:      variant.Currency,
		AmountMin:     variant.SalaryMin,
		AmountMax:     variant.SalaryMax,
		Attributes:    attrs,
		ScrapedAt:     now,
	}

	rawIDPtr := &rp.ID
	if h.deps.VariantStore != nil {
		_ = h.deps.VariantStore.Upsert(ctx, variantstate.Variant{
			VariantID:    eventPayload.VariantID,
			SourceID:     eventPayload.SourceID,
			HardKey:      eventPayload.HardKey,
			Kind:         eventPayload.Kind,
			CurrentStage: variantstate.StageIngested,
			RawPayloadID: rawIDPtr,
		})
	}

	if body, mErr := json.Marshal(eventsv1.NewEnvelope(eventsv1.TopicVariantsIngested, eventPayload)); mErr == nil {
		if h.deps.Inbox != nil {
			if iErr := h.deps.Inbox.Insert(ctx, eventPayload.VariantID, eventPayload.SourceID, body); iErr != nil {
				log.WithError(iErr).Warn("reparse: inbox insert failed")
			}
		} else if pErr := h.deps.Svc.QueueManager().Publish(ctx, h.deps.IngestedQueue, body, nil); pErr != nil {
			log.WithError(pErr).Warn("reparse: publish variant failed")
		}
	}

	_ = h.deps.CrawlRepo.IncrementReparseCount(ctx, rp.ID)
	log.WithField("variant_id", eventPayload.VariantID).Info("reparse: re-emitted variant")
	return nil
}

// enqueueFrontier walks the iterator's items and enqueues each
// SourceURL into the URL frontier. Discovery-mode equivalent of
// the variant-emit loop: instead of running enrichStubs + Verify
// + emit, we hand the URL off to apps/frontier-worker which does
// all of that per-URL under per-host politeness.
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
