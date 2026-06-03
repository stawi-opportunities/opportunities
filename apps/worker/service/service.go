package service

import (
	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/cache"
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

// EmbedWorker returns the SubjectWorkerEmbed subscriber. outQueue is the
// embeddings Queue Name it publishes EmbeddingV1 to.
func (s *Service) EmbedWorker(outQueue string) queue.SubscribeWorker {
	return NewEmbedHandler(s.svc, s.extractor, s.variantStore, outQueue)
}

// TranslateWorker returns the SubjectWorkerTranslate subscriber. outQueue is
// the translations Queue Name it publishes TranslationV1 to.
func (s *Service) TranslateWorker(outQueue string) queue.SubscribeWorker {
	return NewTranslateHandler(s.svc, s.extractor, s.translationLangs, s.variantStore, outQueue)
}
