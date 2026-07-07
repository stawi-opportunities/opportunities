// Package service implements the frontier-worker fetch loop.
//
// The worker consumes crawl.url.enqueued.v1 wake-ups, dequeues
// URLs from the Postgres frontier under per-host politeness, and
// runs the existing extract+emit pipeline for each URL.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"
	"github.com/rs/xid"

	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/content"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/frontier"
	"github.com/stawi-opportunities/opportunities/pkg/jobqueue"
	"github.com/stawi-opportunities/opportunities/pkg/normalize"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
)

// Deps bundles the collaborators the frontier-worker needs.
type Deps struct {
	Svc                *frame.Service
	IngestQueue        *jobqueue.Store
	IngestMaxPending   int64
	IngestMaxOldestAge time.Duration
	Frontier           frontier.Frontier
	Sources            *repository.SourceRepository
	Kinds              *opportunity.Registry
	Extractor          *extraction.Extractor
	Normalizer         *normalize.Normalizer
	Fetcher            *httpx.Client

	// DequeueBatch caps the URLs claimed per Dequeue call.
	DequeueBatch int
	// MaxAttempts is the per-URL retry budget.
	MaxAttempts int
	// IdleTick drives the heartbeat poll fallback so the worker
	// makes progress even when no NATS wake-up fires.
	IdleTick time.Duration
}

// Handler is the frontier-worker top-level. Implements frame.EventI
// against URLEnqueuedV1 wake-ups, and exposes Tick() so the
// heartbeat ticker can fire the same drain loop on its own cadence.
type Handler struct {
	deps     Deps
	workerID string

	// Serialize the dequeue→fetch loop so the heartbeat ticker
	// and the NATS handler don't double-claim the same batch.
	mu sync.Mutex
}

// NewHandler wires the worker.
func NewHandler(deps Deps) *Handler {
	if deps.DequeueBatch <= 0 {
		deps.DequeueBatch = 5
	}
	if deps.MaxAttempts <= 0 {
		deps.MaxAttempts = 5
	}
	if deps.IdleTick <= 0 {
		deps.IdleTick = 5 * time.Second
	}
	return &Handler{
		deps:     deps,
		workerID: podName(),
	}
}

// WorkerID returns the identifier stamped into url_frontier.claimed_by
// for every claim this worker holds.
func (h *Handler) WorkerID() string { return h.workerID }

// Name binds the handler to the URL-enqueued topic.
func (h *Handler) Name() string { return eventsv1.TopicURLEnqueued }

// PayloadType returns *json.RawMessage so Frame skips typed decoding;
// the wake-up event payload is small and we only need it as a nudge.
func (h *Handler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

// Validate accepts any non-empty payload — the event is a nudge,
// not a work item, so even a malformed one shouldn't dead-letter.
// Returning nil here lets Frame ack the wake-up and we'll just
// drain from Postgres on the next call regardless.
func (h *Handler) Validate(_ context.Context, _ any) error { return nil }

// Execute drains as many URLs as Dequeue offers, runs the fetch +
// extract for each, then returns. Multiple frontier-worker pods
// race on Dequeue's SKIP LOCKED claim and never double-claim.
func (h *Handler) Execute(ctx context.Context, _ any) error {
	return h.drain(ctx)
}

// Tick is the heartbeat entry point; called on h.deps.IdleTick
// cadence so the worker makes progress even if a wake-up event
// missed (NATS redelivery delay, JetStream consumer rebalance,
// etc.).
func (h *Handler) Tick(ctx context.Context) {
	if err := h.drain(ctx); err != nil {
		util.Log(ctx).WithError(err).Warn("frontier-worker: tick drain failed")
	}
}

// drainMaxBatches and drainBudget bound a single drain call. The loop
// holds h.mu for its whole duration (taken in Execute/Tick), so an
// unbounded for{} would starve concurrent wake-ups and heartbeat ticks
// while a busy queue keeps yielding batches. Bounding by both a batch
// count and a wall-clock budget lets the loop return so other wake-ups
// can re-enter and re-acquire the lock. Correctness is unaffected: each
// batch is claimed under SELECT ... FOR UPDATE SKIP LOCKED, so returning
// early never double-claims — the next drain just picks up where this
// one left off.
const (
	drainMaxBatches = 20
	drainBudget     = 2 * time.Minute
)

func (h *Handler) drain(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.deps.IngestQueue == nil {
		return fmt.Errorf("PostgreSQL ingest queue is not configured")
	}

	deadline := time.Now().Add(drainBudget)
	for batches := 0; batches < drainMaxBatches; batches++ {
		if time.Now().After(deadline) {
			return nil
		}
		stats, err := h.deps.IngestQueue.Stats(ctx)
		if err != nil {
			return fmt.Errorf("frontier ingest backlog: %w", err)
		}
		if (h.deps.IngestMaxPending > 0 && stats.Pending >= h.deps.IngestMaxPending) ||
			(h.deps.IngestMaxOldestAge > 0 && stats.OldestAge >= h.deps.IngestMaxOldestAge) {
			return nil
		}
		urls, err := h.deps.Frontier.Dequeue(ctx, h.deps.DequeueBatch, h.workerID)
		if err != nil {
			return fmt.Errorf("frontier dequeue: %w", err)
		}
		if len(urls) == 0 {
			return nil
		}
		for _, u := range urls {
			h.runOne(ctx, u)
		}
	}
	return nil
}

// runOne fetches the URL, parses it in memory, runs extraction,
// emits VariantIngestedV1, and Completes the frontier row. On any
// transport / status error the worker calls Fail with the configured
// retry budget so the URL backs off and retries.
func (h *Handler) runOne(ctx context.Context, u frontier.URL) {
	// Per-URL deadline so fetch, storage, extraction, and enqueue cannot hang
	// indefinitely. Must stay below the frontier reclaim lease
	// (staleLeaseSeconds = 15m) so a slow-but-live worker finishes
	// before its claim is reclaimed and double-processed.
	ctx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()

	log := util.Log(ctx).
		WithField("url_id", u.URLID).
		WithField("canonical_url", u.CanonicalURL).
		WithField("source_id", u.SourceID).
		WithField("host", u.Host)

	body, status, err := h.deps.Fetcher.Get(ctx, u.CanonicalURL, nil)
	if err != nil {
		log.WithError(err).Warn("frontier-worker: fetch failed")
		if failErr := h.deps.Frontier.Fail(ctx, u.URLID, err, h.deps.MaxAttempts); failErr != nil {
			log.WithError(failErr).Warn("frontier-worker: Fail failed")
		}
		return
	}
	if status < 200 || status >= 300 {
		statusErr := fmt.Errorf("HTTP %d", status)
		log.WithField("status", status).Warn("frontier-worker: non-2xx")
		if failErr := h.deps.Frontier.Fail(ctx, u.URLID, statusErr, h.deps.MaxAttempts); failErr != nil {
			log.WithError(failErr).Warn("frontier-worker: Fail failed")
		}
		return
	}

	// Load the source so we can stamp source-derived fields
	// (Country, Kinds, etc.) on the emitted variant.
	src, err := h.deps.Sources.GetByID(ctx, u.SourceID)
	if err != nil {
		log.WithError(err).Warn("frontier-worker: source lookup failed")
		_ = h.deps.Frontier.Fail(ctx, u.URLID, err, h.deps.MaxAttempts)
		return
	}
	if src == nil {
		// GetByID reads the replica (readOnly). A nil source here may
		// just be replica lag behind a freshly-created source, so retry
		// rather than Complete — Complete would permanently drop a valid
		// URL. The retry budget bounds how long we wait for the replica
		// to catch up before the row lands in 'failed'.
		notFoundErr := fmt.Errorf("source %s not found (possible replica lag)", u.SourceID)
		log.Warn("frontier-worker: source not found; retrying")
		if failErr := h.deps.Frontier.Fail(ctx, u.URLID, notFoundErr, h.deps.MaxAttempts); failErr != nil {
			log.WithError(failErr).Warn("frontier-worker: Fail failed")
		}
		return
	}

	// Run the AI extractor when configured. The frontier-worker
	// path is single-URL — we run one Extract per URL and emit
	// one VariantIngestedV1.
	var items []domain.ExternalOpportunity
	if h.deps.Extractor != nil {
		kinds := src.Kinds
		if len(kinds) == 0 {
			kinds = []string{"job"}
		}
		// Convert raw HTML to clean markdown before extraction — the
		// extractor sends its document straight to the LLM with no
		// stripping, so a 500 KB listing page is ~150 K tokens and
		// blows past the model context window (observed: "request
		// (152253 tokens) exceeds the available context size (4096
		// tokens)"). Mirrors apps/crawler enrichOne, which runs the
		// same content.ExtractFromHTML pass before Extract.
		docText := string(body)
		if ext, _ := content.ExtractFromHTML(docText); ext != nil && ext.Markdown != "" {
			docText = ext.Markdown
		}
		extracted, exErr := h.deps.Extractor.Extract(ctx, docText, kinds, src.ExtractionPromptExtension)
		if exErr != nil {
			log.WithError(exErr).Warn("frontier-worker: extractor failed")
			if failErr := h.deps.Frontier.Fail(ctx, u.URLID, exErr, h.deps.MaxAttempts); failErr != nil {
				log.WithError(failErr).Warn("frontier-worker: Fail failed")
			}
			return
		}
		if extracted != nil {
			items = []domain.ExternalOpportunity{*extracted}
		}
	} else {
		// No extractor configured — emit a URL stub. Verify
		// will likely reject it, but the audit row still lands.
		items = []domain.ExternalOpportunity{{
			Source:    src.Type,
			SourceURL: u.CanonicalURL,
			ApplyURL:  u.CanonicalURL,
		}}
	}

	var emitErr error
	for i := range items {
		opp := items[i]
		opp.SourceURL = u.CanonicalURL
		if opp.ApplyURL == "" {
			opp.ApplyURL = u.CanonicalURL
		}
		// Country fallback — same chain the crawler runs.
		if src.Country != "" {
			if opp.AnchorLocation == nil {
				opp.AnchorLocation = &domain.Location{Country: src.Country}
			} else if opp.AnchorLocation.Country == "" {
				opp.AnchorLocation.Country = src.Country
			}
		}
		kind := opp.Kind
		if kind == "" && len(src.Kinds) > 0 {
			kind = src.Kinds[0]
			opp.Kind = kind
		}

		// Run Verify if the registry is wired. Rejections are
		// emitted but don't block other items.
		if h.deps.Kinds != nil {
			if res := opportunity.Verify(&opp, src, h.deps.Kinds); !res.OK {
				log.WithField("kind", kind).
					WithField("title", opp.Title).
					Debug("frontier-worker: verify rejected")
				continue
			}
		}

		now := time.Now().UTC()
		var variant normalize.JobVariant
		if h.deps.Normalizer != nil {
			variant = h.deps.Normalizer.Normalize(&opp, src.ID, src.Country, string(src.Type), src.Language, now)
		} else {
			variant = normalize.ExternalToVariant(opp, src.ID, src.Country, string(src.Type), src.Language, now)
		}
		if kind == "" {
			kind = "job"
		}

		attrs := opp.Attributes
		if attrs == nil {
			attrs = map[string]any{}
		}
		attrs["description"] = variant.Description
		attrs["language"] = variant.Language
		attrs["remote_type"] = variant.RemoteType
		attrs["employment_type"] = variant.EmploymentType
		attrs["location_text"] = variant.LocationText
		attrs["content_hash"] = variant.ContentHash
		if variant.PostedAt != nil {
			attrs["posted_at"] = variant.PostedAt.Format(time.RFC3339)
		}

		eventPayload := eventsv1.VariantIngestedV1{
			VariantID:     xid.New().String(),
			SourceID:      src.ID,
			ExternalID:    variant.ExternalID,
			HardKey:       variant.HardKey,
			Kind:          kind,
			Stage:         string(domain.StageRaw),
			Title:         variant.Title,
			ApplyURL:      variant.ApplyURL,
			IssuingEntity: variant.Company,
			AnchorCountry: variant.Country,
			Remote:        variant.RemoteType == "remote",
			Currency:      variant.Currency,
			AmountMin:     variant.SalaryMin,
			AmountMax:     variant.SalaryMax,
			Attributes:    attrs,
			ScrapedAt:     now,
		}

		body, mErr := json.Marshal(eventsv1.NewEnvelope(eventsv1.TopicVariantsIngested, eventPayload))
		if mErr != nil {
			log.WithError(mErr).Warn("frontier-worker: marshal variant failed")
			emitErr = mErr
			continue
		}
		if h.deps.IngestQueue == nil {
			emitErr = fmt.Errorf("PostgreSQL ingest queue is not configured")
			continue
		}
		if err := h.deps.IngestQueue.Enqueue(ctx, jobqueue.EnqueueRequest{
			VariantID: eventPayload.VariantID, SourceID: eventPayload.SourceID,
			IdempotencyKey: "frontier:" + u.URLID + ":" + eventPayload.HardKey,
			Payload:        body,
		}); err != nil {
			log.WithError(err).Warn("frontier-worker: enqueue variant failed")
			emitErr = err
			continue
		}
	}

	// If any required emit failed, the variant never reached the
	// pipeline. Fail (retryable) instead of Complete so we don't
	// silently lose the URL — only Complete when every emit succeeded.
	if emitErr != nil {
		if failErr := h.deps.Frontier.Fail(ctx, u.URLID, emitErr, h.deps.MaxAttempts); failErr != nil {
			log.WithError(failErr).Warn("frontier-worker: Fail failed")
		}
		return
	}

	if err := h.deps.Frontier.Complete(ctx, u.URLID); err != nil {
		log.WithError(err).Warn("frontier-worker: complete failed")
	}
}

// podName returns the pod name from HOSTNAME (K8s convention) or a
// random xid when unset. Used as the worker identifier in
// url_frontier.claimed_by so operators can correlate stuck rows
// with a specific pod.
func podName() string {
	if h := os.Getenv("HOSTNAME"); h != "" {
		return h
	}
	return "frontier-worker-" + xid.New().String()
}
