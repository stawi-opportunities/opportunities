package service

import (
	"github.com/pitabwire/frame/events"
)

// eventI aliases Frame's event handler interface so this file (and the
// group test) doesn't repeat the import path.
type eventI = events.EventI

// HandlersForGroup returns the events-manager handlers this worker
// process should register for the given STAGE_GROUP. Splitting the
// pipeline across groups gives each its own durable consumer
// (independent ack_pending + scaling) so a slow/failing stage can't
// back-pressure the others. embed/translate are subject-queue
// subscribers wired in main.go (see EmbedWorker/TranslateWorker), not
// events-manager handlers, so they are not returned here.
//
// Mapping (keep in sync with the deployment manifests):
//   core     → normalize, dedup (cluster), canonical   (CPU/Valkey/PG, scales high)
//   validate → validate                                (llama-bound, fail-open, capped)
//   publish  → publish                                 (R2; embed/translate wired alongside)
//   all      → every handler on one consumer           (legacy monolith / tests)
//
// Each branch reuses the same constructors as EventHandlers so handler
// construction stays DRY (single source of truth for the deps).
func (s *Service) HandlersForGroup(group string) []eventI {
	switch group {
	case "core":
		return []eventI{
			NewNormalizeHandler(s.svc, s.variantStore),
			NewDedupHandlerWithBackend(s.svc, s.dedupCache, s.clusterCache, s.dedupSkipCache, s.variantStore, s.dedupReadBackend),
			NewCanonicalHandler(s.svc, s.clusterCache, s.variantStore),
		}
	case "validate":
		return []eventI{
			NewValidateHandlerWithSkip(s.svc, s.extractor, s.validationSkipLLM, s.variantStore),
		}
	case "publish":
		return []eventI{
			NewPublishHandler(s.svc, s.publisher, s.registry, s.variantStore),
		}
	case "all":
		return s.EventHandlers()
	default:
		return nil
	}
}
