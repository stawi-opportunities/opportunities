package eventsv1

// Topic names for Phase 1. Additional event types added in later
// phases extend this list. Names follow `{domain}.{noun}.{verb}.v{N}`.
// Breaking schema changes bump the suffix; additive changes bump
// SchemaVersion on the envelope.
const (
	// Opportunity pipeline — variants.
	TopicVariantsIngested   = "opportunities.variants.ingested.v1"
	TopicVariantsNormalized = "opportunities.variants.normalized.v1"
	TopicVariantsValidated  = "opportunities.variants.validated.v1"
	TopicVariantsFlagged    = "opportunities.variants.flagged.v1"
	TopicVariantsClustered  = "opportunities.variants.clustered.v1"

	// Opportunity pipeline — canonicals.
	TopicCanonicalsUpserted = "opportunities.canonicals.upserted.v1"
	TopicCanonicalsExpired  = "opportunities.canonicals.expired.v1"

	// Derived.
	TopicEmbeddings   = "opportunities.embeddings.v1"
	TopicTranslations = "opportunities.translations.v1"
	TopicPublished    = "opportunities.published.v1"

	// Crawl control plane.
	TopicCrawlRequests      = "crawl.requests.v1"
	TopicCrawlPageCompleted = "crawl.page.completed.v1"

	// Source discovery.
	TopicSourcesDiscovered = "sources.discovered.v1"

	// Candidate lifecycle (Phase 5).
	TopicCVUploaded                  = "candidates.cv.uploaded.v1"
	TopicCVExtracted                 = "candidates.cv.extracted.v1"
	TopicCVImproved                  = "candidates.cv.improved.v1"
	TopicCandidateEmbedding          = "candidates.embeddings.v1"
	TopicCandidatePreferencesUpdated = "candidates.preferences.updated.v1"
	TopicCandidateMatchesReady = "candidates.matches.ready.v1"
	// TopicCandidateCVStaleNudge is a notification-only event. The
	// external notification service consumes it to send nudge emails;
	// the writer does NOT persist it to Parquet, so it is intentionally
	// absent from AllTopics() below.
	TopicCandidateCVStaleNudge = "candidates.cv.stale_nudge.v1"
)

// AllTopics returns every topic the writer is expected to subscribe
// to for Phase 1. Kept as a single source of truth so `apps/writer`
// doesn't drift from the declared topics.
func AllTopics() []string {
	return []string{
		TopicVariantsIngested,
		TopicVariantsNormalized,
		TopicVariantsValidated,
		TopicVariantsFlagged,
		TopicVariantsClustered,
		TopicCanonicalsUpserted,
		TopicCanonicalsExpired,
		TopicEmbeddings,
		TopicTranslations,
		TopicPublished,
		TopicCrawlPageCompleted,
		TopicSourcesDiscovered,
		TopicCVUploaded,
		TopicCVExtracted,
		TopicCVImproved,
		TopicCandidateEmbedding,
		TopicCandidatePreferencesUpdated,
		TopicCandidateMatchesReady,
	}
}
