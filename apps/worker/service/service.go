package service

import (
	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/cache"
	"github.com/pitabwire/frame/events"
	"github.com/pitabwire/frame/queue"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/kv"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/publish"
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

	translationLangs []string
}

// NewService ...
func NewService(
	svc *frame.Service,
	ex *extraction.Extractor,
	publisher *publish.R2Publisher,
	registry *opportunity.Registry,
	dedupCache cache.Cache[string, string],
	clusterCache cache.Cache[string, kv.ClusterSnapshot],
	translationLangs []string,
) *Service {
	return &Service{
		svc:              svc,
		extractor:        ex,
		publisher:        publisher,
		registry:         registry,
		dedupCache:       dedupCache,
		clusterCache:     clusterCache,
		translationLangs: translationLangs,
	}
}

// workerIgnoreEvents lists topics that flow through the shared
// svc.opportunities.events stream but the worker does NOT process —
// terminal/audit states, materializer-bound events, CV/candidate
// events handled by apps/matching, and the worker's own outputs.
// Without explicit NoopHandlers, every delivery returns "event not
// found in registry" and NATS never acks, locking the 500-deep
// max_ack_pending window in a redelivery storm.
var workerIgnoreEvents = []string{
	eventsv1.TopicCrawlRequests,
	eventsv1.TopicCrawlPageCompleted,
	eventsv1.TopicSourcesDiscovered,
	eventsv1.TopicSourcesStopped,
	eventsv1.TopicVariantsFlagged,
	eventsv1.TopicVariantsRejected,
	eventsv1.TopicCanonicalsExpired,
	eventsv1.TopicEmbeddings,
	eventsv1.TopicTranslations,
	eventsv1.TopicPublished,
	eventsv1.TopicOpportunityAutoFlagged,
	eventsv1.TopicCVUploaded,
	eventsv1.TopicCVExtracted,
	eventsv1.TopicCVImproved,
	eventsv1.TopicCandidateEmbedding,
	eventsv1.TopicCandidatePreferencesUpdated,
	eventsv1.TopicCandidateMatchesReady,
	eventsv1.TopicCandidateCVStaleNudge,
	eventsv1.TopicCandidateWeeklyJobsDigest,
}

// EventHandlers returns the in-process Frame Event handlers.
//
// These are fast, internal-only stages. External-API stages
// (embed/translate) are returned by QueueWorkers instead.
// NoopHandlers ack-and-drop every workerIgnoreEvents topic so the
// shared events stream stays drained when the worker's stream
// subscription receives event types it isn't responsible for.
func (s *Service) EventHandlers() []events.EventI {
	out := []events.EventI{
		NewNormalizeHandler(s.svc),
		NewValidateHandler(s.svc, s.extractor),
		NewDedupHandlerWithCluster(s.svc, s.dedupCache, s.clusterCache),
		NewCanonicalHandler(s.svc, s.clusterCache),
		NewPublishHandler(s.svc, s.publisher, s.registry),
	}
	for _, t := range workerIgnoreEvents {
		out = append(out, &eventsv1.NoopHandler{Topic: t})
	}
	return out
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
