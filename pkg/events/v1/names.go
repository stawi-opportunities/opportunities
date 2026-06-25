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

	// Source scheduling control plane. Emitted by the api's source
	// lifecycle mutations (approve/pause/resume/stop/start/delete/create/
	// interval-edit) and by the crawler's own admin pause/enable/stop
	// handlers. The crawler's SourceSchedulingHandler consumes it and
	// drives the per-source Trustage schedule to match the source's live
	// status (ensure when active, archive otherwise). Like
	// TopicCrawlRequests / TopicSourcesStopped this is a control-plane
	// event the writer does NOT persist, so it is intentionally absent
	// from AllTopics() below.
	TopicSourceSchedulingChanged = "sources.scheduling.changed.v1"

	// Recipe lifecycle. Emitted by the crawler's generate/regenerate
	// handlers; consumed by RecipeGenerateHandler / RecipeRegenerateHandler.
	// recipe.generate.v1  — synthesise a recipe for a source (onboarding or manual trigger).
	// recipe.regenerate.v1 — drift detected; regenerate and atomically swap when the new recipe passes the gate.
	TopicRecipeGenerate   = "recipe.generate.v1"
	TopicRecipeRegenerate = "recipe.regenerate.v1"

	// Operator-driven kill switch. Emitted by the crawler when an admin
	// calls /admin/sources/stop. The materializer subscribes and
	// removes every Manticore document carrying the matching source_id;
	// the writer persists the event for audit. Downstream consumers
	// must treat it as terminal — the source is no longer crawled,
	// and its historical jobs disappear from search.
	TopicSourcesStopped = "sources.stopped.v1"

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
	TopicCandidateMatchesReady       = "candidates.matches.ready.v1"
	// TopicCandidateCVStaleNudge is a notification-only event. The
	// external notification service consumes it to send nudge emails;
	// the writer does NOT persist it to Parquet, so it is intentionally
	// absent from AllTopics() below.
	TopicCandidateCVStaleNudge = "candidates.cv.stale_nudge.v1"

	// TopicApplicationSubmitted is emitted by the autoapply service after
	// each submission attempt (success, failure, or skip). The writer
	// sinks it to Iceberg for analytics.
	TopicApplicationSubmitted = "candidates.applications.submitted.v1"

	// TopicSessionCaptured is emitted by the matching service when the
	// browser extension uploads a fresh authenticated session for a
	// (candidate, source) pair. Analytics-only.
	TopicSessionCaptured = "candidates.sessions.captured.v1"
	// TopicSessionRequired is emitted by the autoapply handler when an
	// intent arrives but no live session exists for the (candidate,
	// source). The notification service maps this to a "Re-connect your
	// account" CTA in the UI.
	TopicSessionRequired = "candidates.sessions.required.v1"
	// TopicSessionExpired is emitted by the autoapply replay leg when
	// it detects a captured session is no longer accepted by the source
	// (redirect to login, 401, captcha). Triggers UI re-onboarding and
	// records the failure for analytics.
	TopicSessionExpired = "candidates.sessions.expired.v1"
	// TopicProfileIncomplete is emitted by the autoapply handler when an
	// apply is skipped because the candidate's on-source profile (e.g.
	// BrighterMonday, Jobberman) is missing required apply-form fields.
	// The notification service maps this to a "Complete your <source>
	// profile" CTA in the UI.
	TopicProfileIncomplete = "candidates.profile.incomplete.v1"

	// TopicCandidateWeeklyJobsDigest is a notification-only event
	// targeting candidates who completed signup/onboarding but have
	// not finished checkout (Subscription IN free/trial/cancelled OR
	// SubscriptionID empty). Emitted once per such candidate by the
	// Trustage weekly cron in apps/matching. Carries the top-N freshest
	// jobs in the candidate's country + opted-in kinds plus headline
	// analytics for the past 7 days. The external notification service
	// renders the digest email; the writer does NOT persist this event
	// to Parquet, so it is intentionally absent from AllTopics() below.
	TopicCandidateWeeklyJobsDigest = "candidates.weekly_jobs_digest.v1"
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

	// SubjectMatchingDeadletter receives matching events that exceeded the
	// redelivery budget. Admin /api/admin/dlq/replay re-publishes them.
	SubjectMatchingDeadletter = "svc.opportunities.matching.deadletter"
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
		// TopicSourcesStopped is intentionally NOT persisted to Parquet
		// today — the materializer cascade-delete is the load-bearing
		// behaviour; audit trails live in the materializer's INFO logs
		// plus the sources.last_stopped_at / last_stopped_by columns
		// in Postgres. Add an arrow schema + BuildSourceStoppedRecord
		// in apps/writer when permanent audit is required.
		TopicCVUploaded,
		TopicCVExtracted,
		TopicCVImproved,
		TopicCandidateEmbedding,
		TopicCandidatePreferencesUpdated,
		TopicCandidateMatchesReady,
		TopicOpportunityAutoFlagged,
		TopicApplicationSubmitted,
		TopicSessionCaptured,
		TopicSessionRequired,
		TopicSessionExpired,
		TopicProfileIncomplete,
		TopicDefinitionsChanged,
	}
}
