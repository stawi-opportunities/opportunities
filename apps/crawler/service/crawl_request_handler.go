package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"
	"github.com/rs/xid"

	"stawi.jobs/pkg/archive"
	"stawi.jobs/pkg/connectors"
	"stawi.jobs/pkg/content"
	"stawi.jobs/pkg/domain"
	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/extraction"
	"stawi.jobs/pkg/normalize"
	"stawi.jobs/pkg/quality"
)

// SourceGetter is the narrow repository slice the crawl-request handler
// needs. Satisfied by *repository.SourceRepository in production.
type SourceGetter interface {
	GetByID(ctx context.Context, id string) (*domain.Source, error)
}

// CrawlRequestDeps bundles the handler's collaborators so construction
// stays one-shot and tests can inject fakes without ceremony.
type CrawlRequestDeps struct {
	Svc       *frame.Service
	Sources   SourceGetter
	Registry  *connectors.Registry
	Archive   archive.Archive
	Extractor *extraction.Extractor // nil → skip AI enrichment
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
	if src.Status != domain.SourceActive && src.Status != domain.SourceDegraded {
		h.emitCompleted(ctx, eventsv1.CrawlPageCompletedV1{
			RequestID: req.RequestID,
			SourceID:  req.SourceID,
			URL:       src.BaseURL,
			ErrorCode: "source_not_eligible",
		})
		return nil
	}

	conn, ok := h.deps.Registry.Get(src.Type)
	if !ok {
		h.emitCompleted(ctx, eventsv1.CrawlPageCompletedV1{
			RequestID: req.RequestID,
			SourceID:  req.SourceID,
			URL:       src.BaseURL,
			ErrorCode: "connector_not_registered",
		})
		log.WithField("source_type", src.Type).Warn("crawl.request: no connector")
		return nil
	}

	iter := conn.Crawl(ctx, *src)

	var (
		jobsFound    int
		jobsEmitted  int
		jobsRejected int
		lastCursor   string
		iterErr      error
		status       = http.StatusOK
	)

	for iter.Next(ctx) {
		status = iter.HTTPStatus()
		pageArchiveRef := resolveArchiveRef(ctx, h.deps.Archive, iter.Content())
		for _, extJob := range iter.Jobs() {
			jobsFound++

			// Apply-URL fallback chain first — normalize and quality both see the
			// resolved URL.
			quality.EnsureApplyURL(&extJob, extJob.SourceURL)
			if extJob.ApplyURL == "" {
				quality.EnsureApplyURL(&extJob, src.BaseURL)
			}

			// Deterministic quality gate — same check the legacy crawler ran.
			if qErr := quality.Check(extJob); qErr != nil {
				jobsRejected++
				continue
			}

			// Convert to a VariantIngested payload via the existing normalize
			// helper. Hard-key, stage, and mapping live there.
			now := time.Now().UTC()
			variant := normalize.ExternalToVariant(
				extJob, src.ID, src.Country, string(src.Type), src.Language, now,
			)

			// Build the event payload from the domain variant. Fields
			// the pipeline does not need at ingest (extended JobFields,
			// intelligence signals) land in later events — spec §5.2
			// keeps variant-ingested narrow.
			eventPayload := eventsv1.VariantIngestedV1{
				VariantID:      xid.New().String(),
				SourceID:       src.ID,
				ExternalID:     variant.ExternalJobID,
				HardKey:        variant.HardKey,
				Stage:          string(domain.StageRaw),
				Title:          variant.Title,
				Company:        variant.Company,
				LocationText:   variant.LocationText,
				Country:        variant.Country,
				Language:       variant.Language,
				RemoteType:     variant.RemoteType,
				EmploymentType: variant.EmploymentType,
				SalaryMin:      variant.SalaryMin,
				SalaryMax:      variant.SalaryMax,
				Currency:       variant.Currency,
				Description:    variant.Description,
				ApplyURL:       variant.ApplyURL,
				ScrapedAt:      now,
				ContentHash:    variant.ContentHash,
				RawArchiveRef:  pageArchiveRef,
			}
			if variant.PostedAt != nil {
				eventPayload.PostedAt = *variant.PostedAt
			}

			if emitErr := evtMgr.Emit(ctx, eventsv1.TopicVariantsIngested,
				eventsv1.NewEnvelope(eventsv1.TopicVariantsIngested, eventPayload),
			); emitErr != nil {
				log.WithError(emitErr).Warn("crawl.request: emit variant failed")
				continue
			}
			jobsEmitted++
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
	}

	if e := iter.Err(); e != nil {
		iterErr = e
		log.WithError(e).Warn("crawl.request: iterator failed")
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
		return ""
	}
	return archive.RawKey(hash)
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
