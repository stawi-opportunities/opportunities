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
	TopicVariantsRejected   = "opportunities.variants.rejected.v1"

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

	// User-driven scam-flagging auto-action. Emitted by the api when
	// ≥ domain.FlagAutoActionThreshold distinct scam flags accumulate
	// on a slug. The materializer hides the row from search.
	TopicOpportunityAutoFlagged = "opportunities.auto_flagged.v1"

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

	// TopicApplicationSubmitted is emitted by the autoapply service after
	// each submission attempt (success, failure, or skip). The writer
	// sinks it to Iceberg for analytics.
	TopicApplicationSubmitted = "candidates.applications.submitted.v1"
)

// Queue subject names for the durable, retry-safe handlers (per the
// Frame async decision tree, external-API calls live on Frame Queue
// not Frame Events). Each subject maps to a NATS JetStream subject
// under the matching/worker streams (see deployment manifests). The
// payload format on each is the same envelope as the equivalent
// event topic — only the transport differs.
const (
	// Candidate CV pipeline (apps/matching). The cv-uploaded ingress
	// stays an HTTP-driven publish; cv-extract / cv-improve / cv-embed
	// are durable queue consumers that call external LLM endpoints.
	SubjectCVExtract = "svc.opportunities.matching.cv.extract.v1"
	SubjectCVImprove = "svc.opportunities.matching.cv.improve.v1"
	SubjectCVEmbed   = "svc.opportunities.matching.cv.embed.v1"

	// Canonical fan-out (apps/worker). Embed and translate fire
	// independently after a canonical is upserted. Each is its own
	// durable subscriber so a transient embed/translate provider
	// outage doesn't block the publish path.
	SubjectWorkerEmbed     = "svc.opportunities.worker.embed.v1"
	SubjectWorkerTranslate = "svc.opportunities.worker.translate.v1"

	// SubjectAutoApplySubmit is published by apps/matching when a
	// qualified match is ready for auto-apply, consumed by apps/autoapply
	// which executes the browser submission and records the result.
	SubjectAutoApplySubmit = "svc.opportunities.autoapply.submit.v1"
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
		TopicVariantsRejected,
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
		TopicOpportunityAutoFlagged,
		TopicApplicationSubmitted,
	}
}
