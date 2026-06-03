package service

import (
	"github.com/pitabwire/frame/queue"
)

// Per-stage Frame Queue workers. Each pipeline stage is a native
// queue.SubscribeWorker that consumes its one upstream queue and publishes
// to the next by Name (service-profile idiom). main.go registers the
// subscriber/publisher pairs for the stages in its STAGE_GROUP, passing the
// downstream queue Names from config. No events bus, no catch-all.

// NormalizeWorker consumes ingested and publishes VariantNormalizedV1 to
// the next (normalized) queue.
func (s *Service) NormalizeWorker(next string) queue.SubscribeWorker {
	return NewNormalizeHandler(s.svc, s.variantStore, next)
}

// ValidateWorker consumes normalized and publishes VariantValidatedV1
// (or VariantFlaggedV1) to the given queues.
func (s *Service) ValidateWorker(nextValidated, nextFlagged string) queue.SubscribeWorker {
	return NewValidateHandlerWithSkip(s.svc, s.extractor, s.validationSkipLLM, s.variantStore, nextValidated, nextFlagged)
}

// DedupWorker consumes validated and publishes VariantClusteredV1.
func (s *Service) DedupWorker(next string) queue.SubscribeWorker {
	return NewDedupHandlerWithBackend(s.svc, s.dedupCache, s.clusterCache, s.dedupSkipCache, s.variantStore, s.dedupReadBackend, next)
}

// CanonicalWorker consumes clustered and publishes CanonicalUpsertedV1
// (and fans out to the embed/translate queues).
func (s *Service) CanonicalWorker(next string) queue.SubscribeWorker {
	return NewCanonicalHandler(s.svc, s.clusterCache, s.variantStore, next)
}

// PublishWorker consumes canonical and publishes PublishedV1.
func (s *Service) PublishWorker(next string) queue.SubscribeWorker {
	return NewPublishHandler(s.svc, s.publisher, s.registry, s.variantStore, next)
}
