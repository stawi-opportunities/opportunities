package service

import (
	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/cache"
	"github.com/pitabwire/frame/events"
	"github.com/pitabwire/frame/queue"

	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/kv"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/publish"
	"github.com/stawi-opportunities/opportunities/pkg/variantstate"
)

// Service is the worker's composition root.
//
// The pipeline mixes Frame Events (fast in-process work) and Frame
// Queue (durable retry-safe external I/O) per the Frame async decision
// tree:
//
//   - Events: normalize, validate, dedup, canonical, publish — all
//     fast and process-local. They use Frame's events bus for low-
//     latency chaining.
//   - Queue:  embed + translate — both call external LLM endpoints
//     (TEI / Groq) that may take seconds and may fail; durable retry
//     with backoff is mandatory.
//
// The canonical handler emits the events-bus event AND publishes to
// the queue subjects so the fan-out is explicit (canonical-publish
// runs in process; embed/translate run with full retry semantics).
type Service struct {
	svc       *frame.Service
	extractor *extraction.Extractor
	publisher *publish.R2Publisher
	registry  *opportunity.Registry

	dedupCache   cache.Cache[string, string]
	clusterCache cache.Cache[string, kv.ClusterSnapshot]

	// pipeline_variants ledger (Postgres). nil when DATABASE_URL is
	// absent — handlers degrade to NATS+Valkey only and skip the
	// observability writes. See pkg/variantstate for the soft-fail
	// guarantees.
	variantStore *variantstate.Store

	translationLangs  []string
	validationSkipLLM bool
	dedupSkipCache    bool
	dedupReadBackend  string
}

// NewService ...
func NewService(
	svc *frame.Service,
	ex *extraction.Extractor,
	publisher *publish.R2Publisher,
	registry *opportunity.Registry,
	dedupCache cache.Cache[string, string],
	clusterCache cache.Cache[string, kv.ClusterSnapshot],
	variantStore *variantstate.Store,
	translationLangs []string,
	validationSkipLLM bool,
	dedupSkipCache bool,
	dedupReadBackend string,
) *Service {
	return &Service{
		svc:               svc,
		extractor:         ex,
		publisher:         publisher,
		registry:          registry,
		dedupCache:        dedupCache,
		clusterCache:      clusterCache,
		variantStore:      variantStore,
		translationLangs:  translationLangs,
		validationSkipLLM: validationSkipLLM,
		dedupSkipCache:    dedupSkipCache,
		dedupReadBackend:  dedupReadBackend,
	}
}

// EventHandlers returns the in-process Frame Event handlers.
//
// These are fast, internal-only stages. External-API stages
// (embed/translate) are returned by QueueWorkers instead.
//
// The worker subscribes to the catch-all svc.opportunities.events.>
// stream subject. Frame v1.97.3 loose-mode (configured via the
// main.go wiring: svc.EventsManager().SetStrict(false)) acks-and-skips
// any event whose Name() isn't a registered handler below, so the
// previous per-topic NoopHandler block is no longer needed.
func (s *Service) EventHandlers() []events.EventI {
	return []events.EventI{
		NewNormalizeHandler(s.svc, s.variantStore),
		NewValidateHandlerWithSkip(s.svc, s.extractor, s.validationSkipLLM, s.variantStore),
		NewDedupHandlerWithBackend(s.svc, s.dedupCache, s.clusterCache, s.dedupSkipCache, s.variantStore, s.dedupReadBackend),
		NewCanonicalHandler(s.svc, s.clusterCache, s.variantStore),
		NewPublishHandler(s.svc, s.publisher, s.registry, s.variantStore),
	}
}

// EmbedWorker returns the queue subscriber for SubjectWorkerEmbed.
// The caller registers it via frame.WithRegisterSubscriber.
func (s *Service) EmbedWorker() queue.SubscribeWorker {
	return NewEmbedHandler(s.svc, s.extractor)
}

// TranslateWorker returns the queue subscriber for SubjectWorkerTranslate.
func (s *Service) TranslateWorker() queue.SubscribeWorker {
	return NewTranslateHandler(s.svc, s.extractor, s.translationLangs)
}

// Handlers is retained for backwards compatibility with existing
// pipeline tests that exercise the events-bus chain. It returns just
// the event handlers (matching the previous behaviour after the
// embed/translate/publish fanout split — publish remains here).
//
// Deprecated: prefer EventHandlers + EmbedWorker + TranslateWorker.
func (s *Service) Handlers() []events.EventI {
	return s.EventHandlers()
}
