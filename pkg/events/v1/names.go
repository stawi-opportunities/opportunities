package eventsv1

// Topic names for Phase 1. Additional event types added in later
// phases extend this list. Names follow `{domain}.{noun}.{verb}.v{N}`.
// Breaking schema changes bump the suffix; additive changes bump
// SchemaVersion on the envelope.
const (
	// Opportunity pipeline — variants.
	TopicVariantsIngested = "opportunities.variants.ingested.v1"

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
	// TopicCrawlRequests this is a control-plane event.
	TopicSourceSchedulingChanged = "sources.scheduling.changed.v1"

	// Recipe lifecycle. Emitted by the crawler's generate/regenerate
	// handlers; consumed by RecipeGenerateHandler / RecipeRegenerateHandler.
	// recipe.generate.v1  — synthesise a recipe for a source (onboarding or manual trigger).
	// recipe.regenerate.v1 — drift detected; regenerate and atomically swap when the new recipe passes the gate.
	TopicRecipeGenerate   = "recipe.generate.v1"
	TopicRecipeRegenerate = "recipe.regenerate.v1"

	// Candidate lifecycle (Phase 5).
	TopicCVUploaded                  = "candidates.cv.uploaded.v1"
	TopicCVExtracted                 = "candidates.cv.extracted.v1"
	TopicCVImproved                  = "candidates.cv.improved.v1"
	TopicCandidateEmbedding          = "candidates.embeddings.v1"
	TopicCandidatePreferencesUpdated = "candidates.preferences.updated.v1"
	TopicCandidateMatchesReady       = "candidates.matches.ready.v1"
	// TopicCandidateCVStaleNudge is a notification-only event. The
	// external notification service consumes it to send nudge emails.
	TopicCandidateCVStaleNudge = "candidates.cv.stale_nudge.v1"

	// TopicCandidateWeeklyJobsDigest is a notification-only event
	// targeting candidates who completed signup/onboarding but have
	// not finished checkout (Subscription IN free/trial/cancelled OR
	// SubscriptionID empty). Emitted once per such candidate by the
	// Trustage weekly cron in apps/matching. Carries the top-N freshest
	// jobs in the candidate's country + opted-in kinds plus headline
	// analytics for the past 7 days. The external notification service
	// renders the digest email.
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

	// SubjectMatchingDeadletter receives matching events that exceeded the
	// redelivery budget. Admin /api/admin/dlq/replay re-publishes them.
	SubjectMatchingDeadletter = "svc.opportunities.matching.deadletter"

	// Opportunity pipeline stages that call external services (Frame Queue).
	// SubjectWorkerEmbed: after postgres Complete, the ingest worker publishes
	// OpportunityEmbedV1 here; EmbedHandler computes the vector and writes
	// opportunities.embedding. Durable + retried (external embedding HTTP).
	SubjectWorkerEmbed = "svc.opportunities.worker.embed.v1"

	// SubjectOpportunityFanOut: after embed succeeds, the worker publishes
	// OpportunityFanOutV1 with the vector; matching runs Path A FanOut so
	// new jobs land in candidate_matches as they arrive.
	SubjectOpportunityFanOut = "svc.opportunities.matching.opportunity.fanout.v1"
)
