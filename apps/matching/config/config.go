package config

import (
	fconfig "github.com/pitabwire/frame/v2/config"
)

// CandidatesConfig embeds Frame's ConfigurationDefault and adds
// candidate-service-specific settings.
type CandidatesConfig struct {
	fconfig.ConfigurationDefault

	// Cloudflare R2 — one account token authorised on all three
	// product-opportunities buckets. Matching uses the archive
	// bucket only for raw CV bytes uploaded by candidates.
	R2AccountID       string `env:"R2_ACCOUNT_ID"        envDefault:""`
	R2AccessKeyID     string `env:"R2_ACCESS_KEY_ID"     envDefault:""`
	R2SecretAccessKey string `env:"R2_SECRET_ACCESS_KEY" envDefault:""`
	R2Endpoint        string `env:"R2_ENDPOINT"          envDefault:""`
	R2ArchiveBucket   string `env:"R2_ARCHIVE_BUCKET"    envDefault:"product-opportunities-archive"`

	// AI inference back-end (OpenAI-compatible).
	InferenceBaseURL string `env:"INFERENCE_BASE_URL" envDefault:""`
	InferenceAPIKey  string `env:"INFERENCE_API_KEY"  envDefault:""`
	InferenceModel   string `env:"INFERENCE_MODEL"    envDefault:""`

	// Embeddings — optional, graceful no-op when empty.
	EmbeddingBaseURL string `env:"EMBEDDING_BASE_URL" envDefault:""`
	EmbeddingAPIKey  string `env:"EMBEDDING_API_KEY"  envDefault:""`
	EmbeddingModel   string `env:"EMBEDDING_MODEL"    envDefault:""`
	// EmbeddingDimensions pins the embeddings "dimensions" field (Qwen3 MRL);
	// 0 omits it. Must equal EMBEDDING_DIM.
	EmbeddingDimensions int `env:"EMBEDDING_DIMENSIONS" envDefault:"0"`
	// EmbeddingInputType: "passage"/"query" for NVIDIA asymmetric E5; empty omits.
	EmbeddingInputType string `env:"EMBEDDING_INPUT_TYPE" envDefault:""`

	// Reranker (cross-encoder, e.g. BAAI/bge-reranker-v2-m3 via TEI).
	// Matcher falls back to retrieval-order when unset.
	RerankBaseURL string `env:"RERANK_BASE_URL" envDefault:""`
	RerankAPIKey  string `env:"RERANK_API_KEY"  envDefault:""`
	RerankModel   string `env:"RERANK_MODEL"    envDefault:""`
	// RerankDialect: "tei" (default) or "openai"/"siliconflow" for /v1/rerank.
	RerankDialect string `env:"RERANK_DIALECT" envDefault:""`

	// Matching-stage feature flags.
	RerankEnabled     bool    `env:"RERANK_ENABLED"      envDefault:"false"`
	RerankSampleRatio float64 `env:"RERANK_SAMPLE_RATIO" envDefault:"1.0"`
	RerankTopK        int     `env:"RERANK_TOP_K"        envDefault:"100"`

	// OpportunityKindsDir is the directory holding the opportunity-kinds YAML
	// registry. Mounted as a ConfigMap in production at this path.
	OpportunityKindsDir string `env:"OPPORTUNITY_KINDS_DIR" envDefault:"/etc/opportunity-kinds"`

	// CV-pipeline queue subject URLs. The cv-extract / cv-improve /
	// cv-embed handlers are durable Frame Queue subscribers (per the
	// async decision tree: external LLM calls + long-running work →
	// Queue, not Events). Each maps to its own NATS subject under
	// the svc_opportunities_matching stream so per-stage backpressure
	// + dead-letter behaviour is independent. Empty defaults to the
	// in-memory driver so local dev / tests work without NATS.
	CVExtractQueueURL string `env:"CV_EXTRACT_QUEUE_URL" envDefault:"mem://svc.opportunities.matching.cv.extract.v1"`
	CVImproveQueueURL string `env:"CV_IMPROVE_QUEUE_URL" envDefault:"mem://svc.opportunities.matching.cv.improve.v1"`
	CVEmbedQueueURL   string `env:"CV_EMBED_QUEUE_URL"   envDefault:"mem://svc.opportunities.matching.cv.embed.v1"`

	// Candidate-embedding queue: cv-embed publishes CandidateEmbeddingV1 here;
	// the candidate-change consumer drains it for gap-fill + rerank. Dedicated
	// durable queue (not the shared events bus) so the flow is isolated + robust.
	CandidateEmbeddingQueueURI  string `env:"CANDIDATE_EMBEDDING_QUEUE_URI"  envDefault:"mem://candidate_embedding"`
	CandidateEmbeddingQueueName string `env:"CANDIDATE_EMBEDDING_QUEUE_NAME" envDefault:"candidate_embedding"`

	// Opportunity fan-out (Path A): worker publishes OpportunityFanOutV1 after
	// embed; this consumer runs FanOut so matches collect as jobs arrive.
	// Production sets OPPORTUNITY_FANOUT_QUEUE_URI to the NATS workqueue DSN.
	// Default mem:// is for local/dev only.
	OpportunityFanOutQueueURI  string `env:"OPPORTUNITY_FANOUT_QUEUE_URI"  envDefault:"mem://svc.opportunities.matching.opportunity.fanout.v1"`
	OpportunityFanOutQueueName string `env:"OPPORTUNITY_FANOUT_QUEUE_NAME" envDefault:"svc.opportunities.matching.opportunity.fanout.v1"`
	// MatchingFanOutEnabled defaults ON. Set false to stop live Path A.
	MatchingFanOutEnabled bool `env:"MATCHING_FANOUT_ENABLED" envDefault:"true"`

	// PlansURL is embedded into the weekly-jobs-digest event so the
	// notification service's email template doesn't have to assume the
	// host. Defaults to production; preview deploys override via env.
	PlansURL string `env:"PLANS_URL" envDefault:"https://jobs.stawi.org/pricing/"`

	// Digest schedule (who receives summaries on each Trustage fire).
	// Trustage cron_expr in definitions/trustage/*-digest.json is the
	// wall-clock schedule (edit those JSON schedules to change when the
	// job runs). These env vars control recipient filtering:
	//   DIGEST_DEFAULT_CADENCE = auto|daily|weekly
	//   DIGEST_WEEKLY_WEEKDAY  = monday…sunday (or 0–6, Sunday=0)
	//   DIGEST_TIMEZONE        = IANA zone for weekday evaluation
	DigestDefaultCadence string `env:"DIGEST_DEFAULT_CADENCE" envDefault:"auto"`
	DigestWeeklyWeekday  string `env:"DIGEST_WEEKLY_WEEKDAY"  envDefault:"monday"`
	DigestTimezone       string `env:"DIGEST_TIMEZONE"        envDefault:"UTC"`

	// ValkeyURL is the Valkey/Redis connection URL for the distributed debouncer.
	// When empty (default) the in-memory MemoryDebouncer is used, which is safe
	// for dev/test but does not survive restarts or span multiple replicas.
	ValkeyURL string `env:"VALKEY_URL" envDefault:""`

	// Phase-2 continuous matching pipeline. Defaults ON so a paid user
	// gets gap-fill matches after CV embed without an extra config flip.
	// Set MATCHING_CANDIDATE_CHANGE_ENABLED=false to disable in emergency.
	MatchingCandidateChangeEnabled bool `env:"MATCHING_CANDIDATE_CHANGE_ENABLED" envDefault:"true"`
	// MatchingMinScore is the default cosine-combined score floor for new
	// candidate_match_indexes rows (0–1). Only opportunities scoring at or
	// above this threshold become matches. Per-candidate rules can raise it.
	MatchingMinScore           float64 `env:"MATCHING_MIN_SCORE" envDefault:"0.45"`
	MatchingRerankerEnabled    bool    `env:"MATCHING_RERANKER_ENABLED"         envDefault:"false"`
	MatchingDLQThreshold       int     `env:"MATCHING_DLQ_THRESHOLD"            envDefault:"5"`
	MatchingDebounceTTLSeconds int     `env:"MATCHING_DEBOUNCE_TTL_SECONDS"     envDefault:"60"`
	// PooledReranker bounds: a cloud cross-encoder over RERANK_TOP_K docs
	// takes seconds, so the per-call timeout must be generous (the old
	// hardcoded 1s timed out → reranker silently fell back to bi-encoder).
	MatchingRerankerTimeoutSeconds int `env:"MATCHING_RERANKER_TIMEOUT_SECONDS" envDefault:"30"`
	MatchingRerankerConcurrency    int `env:"MATCHING_RERANKER_CONCURRENCY"     envDefault:"8"`
	// Phase-4 extension-facing /api/me/* routes (rules, matches poll).
	MatchingExtensionEnabled bool `env:"MATCHING_EXTENSION_ENABLED" envDefault:"true"`

	// AuthRequireJWT refuses to boot when no OIDC authenticator is
	// configured. Production must set this true so /me/* never falls
	// open to X-Candidate-ID spoofing.
	AuthRequireJWT bool `env:"AUTH_REQUIRE_JWT" envDefault:"false"`

	// AdminSharedSecret authenticates Trustage / machine callers of
	// /_admin/* via X-Admin-Token (or Authorization: Bearer). Required
	// in production alongside or instead of admin-role JWTs.
	AdminSharedSecret string `env:"ADMIN_SHARED_SECRET" envDefault:""`

	// Billing / payments. BillingServiceURI points at the co-deployed
	// service-payment + service-billing pod (set live to
	// http://service-payment.finance.svc:80). When empty the billing
	// routes serve the plan catalog + degrade checkout to a 503 (NopGateway)
	// so the binary still boots without a payment backend in dev/test.
	BillingServiceURI string `env:"BILLING_SERVICE_URI" envDefault:""`
	// FileServiceURI is the antinvestor files Connect endpoint. When set,
	// CV uploads are stored via the platform files service; when empty,
	// matching falls back to the product R2 archive bucket.
	FileServiceURI string `env:"FILE_SERVICE_URI" envDefault:""`
	// BillingWebhookSecret enables HMAC-SHA256 verification of the
	// service-payment completion webhook (X-Payment-Signature header over
	// the raw body). Empty disables verification (dev/test only).
	BillingWebhookSecret string `env:"BILLING_WEBHOOK_SECRET" envDefault:""`
	// BillingReconcileBatch bounds how many pending checkouts one
	// POST /_admin/billing/reconcile sweep examines.
	BillingReconcileBatch int `env:"BILLING_RECONCILE_BATCH" envDefault:"200"`
	// PublicSiteURL is the candidate SPA origin used as return_url after
	// hosted checkout (…/dashboard/?billing=success).
	PublicSiteURL string `env:"PUBLIC_SITE_URL" envDefault:"https://opportunities.stawi.org"`
	// CheckoutServiceURI is the base for checkout's internal session API
	// (cluster DNS, e.g. http://service-payment-checkout.finance.svc:80).
	// When set, CreateCheckout opens pay.stawi.org embedded card checkout
	// (Stripe Link style) instead of a Flutterwave multipay redirect.
	CheckoutServiceURI string `env:"CHECKOUT_SERVICE_URI" envDefault:""`
	// CheckoutInternalToken authenticates POST /internal/v1/sessions on checkout.
	// Must match CHECKOUT_INTERNAL_TOKEN or CHECKOUT_SIGNING_SECRET there.
	CheckoutInternalToken string `env:"CHECKOUT_INTERNAL_TOKEN" envDefault:""`
	// CheckoutPublicBaseURL is the public pay page origin (page URL fallback).
	CheckoutPublicBaseURL string `env:"CHECKOUT_PUBLIC_BASE_URL" envDefault:"https://pay.stawi.org"`
}
