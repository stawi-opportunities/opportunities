// Package config loads apps/worker runtime configuration.
package config

import (
	"time"

	fconfig "github.com/pitabwire/frame/config"
)

// Config for apps/worker. Frame base handles Postgres + pub/sub +
// OTEL; this struct adds JetStream KV (via Frame's cache framework),
// R2 publish, LLM backends, and translation configuration.
type Config struct {
	fconfig.ConfigurationDefault

	// ValkeyDSN is the Valkey/Redis URL backing dedup (hard_key →
	// cluster_id) and cluster-snapshot storage. Format:
	// redis://host:port (no auth) or redis://:password@host:port.
	// When set, the worker uses frame/cache/valkey instead of the
	// JetStream-KV backend below. We moved off NATS-KV because of
	// pathological 5s/op timeouts that stalled the canonical chain
	// (see deployment.manifests workspace memory on the symptom).
	ValkeyDSN string `env:"VALKEY_DSN"`

	// Legacy JetStream-KV settings. Used only when ValkeyDSN is empty
	// — kept around so existing dev/test envs without Valkey continue
	// to work and so the cutover stays reversible if Valkey hits its
	// own problems.
	CacheNATSURL       string `env:"CACHE_NATS_URL"`
	CacheNATSCredsFile string `env:"CACHE_NATS_CREDS_FILE"`

	// CacheBucket is the cache namespace name. With Valkey it's the
	// keyspace prefix that Frame's typed-cache views inherit; with
	// JetStream-KV it's the bucket name.
	CacheBucket string `env:"CACHE_BUCKET" envDefault:"opportunities-worker"`

	// Cloudflare R2 — one account token authorised on all three
	// product-opportunities buckets. Worker uses the content bucket
	// for slug-direct snapshots written by pkg/publish and scanned
	// by /_admin/kv/rebuild.
	R2AccountID       string `env:"R2_ACCOUNT_ID,required"`
	R2AccessKeyID     string `env:"R2_ACCESS_KEY_ID,required"`
	R2SecretAccessKey string `env:"R2_SECRET_ACCESS_KEY,required"`
	R2Endpoint        string `env:"R2_ENDPOINT" envDefault:""`

	// R2ContentBucket is the consumer-facing bucket the worker writes
	// snapshots to and scans on /_admin/kv/rebuild.
	R2ContentBucket string `env:"R2_CONTENT_BUCKET" envDefault:"product-opportunities-content"`

	// AI backends. All optional — empty disables the given stage
	// gracefully (fall-through without that AI call).
	InferenceBaseURL string `env:"INFERENCE_BASE_URL"`
	InferenceAPIKey  string `env:"INFERENCE_API_KEY"`
	InferenceModel   string `env:"INFERENCE_MODEL"`
	EmbeddingBaseURL string `env:"EMBEDDING_BASE_URL"`
	EmbeddingAPIKey  string `env:"EMBEDDING_API_KEY"`
	EmbeddingModel   string `env:"EMBEDDING_MODEL"`
	// EmbeddingDimensions pins the embeddings "dimensions" field (Qwen3 MRL);
	// 0 omits it. Must equal EMBEDDING_DIM.
	EmbeddingDimensions int `env:"EMBEDDING_DIMENSIONS" envDefault:"0"`

	// Translation target languages. Empty → translator is a no-op.
	// Pipe-separated: "en|sw|fr".
	TranslationLangs []string `env:"TRANSLATION_LANGS" envSeparator:"|"`

	// Minimum validation confidence to mark a variant "validated"
	// (below goes to flagged). Matches the existing handler.
	ValidationMinConfidence float64 `env:"VALIDATION_MIN_CONFIDENCE" envDefault:"0.7"`

	// Validation LLM request timeout.
	ValidationTimeout time.Duration `env:"VALIDATION_TIMEOUT" envDefault:"30s"`

	// ValidationSkipLLM short-circuits the validation LLM call and
	// emits VariantValidated@1.0 confidence for every record. Use when
	// the shared llama fleet is saturated by other consumers (crawler
	// enrichStubs, embed/translate) and validate is bottlenecking
	// the canonical chain: variants.ingested → normalize → validate
	// (LLM) → dedup → canonical → publish. The canonical chain
	// produces the canonicals.upserted events that the materializer
	// turns into Manticore rows — so a stalled validate keeps the
	// /jobs page empty even when the rest of the pipeline is healthy.
	// Connector-supplied fields are already the authoritative data
	// path for greenhouse/themuse/arbeitnow/etc., so skipping the
	// LLM QC step is a clean way to trade quality scoring for
	// throughput.
	ValidationSkipLLM bool `env:"VALIDATION_SKIP_LLM" envDefault:"false"`

	// DedupSkipCache bypasses the JetStream-KV dedup lookup and uses
	// HardKey directly as the cluster_id. Use when KV operations are
	// timing out under load (5s per call × four calls per dedup =
	// ~20s wall-time per variant; with max_ack_pending=32 throughput
	// caps near 1.6 msgs/sec — Manticore stays effectively empty).
	// Trade-off: lose dedup so a job posted at two sources or seen on
	// two crawls produces two clusters. Acceptable for getting jobs
	// onto the website while we diagnose the NATS-KV latency.
	DedupSkipCache bool `env:"DEDUP_SKIP_CACHE" envDefault:"false"`

	// DedupReadBackend controls which store DedupHandler consults first
	// to resolve hard_key → cluster_id. Phase 4 cutover values:
	//
	//   "valkey"   — Valkey first, Postgres fallback (Phase 4a, default)
	//   "postgres" — Postgres first, Valkey fallback (Phase 4b)
	//   "postgres-only" — Postgres only, no Valkey reads or writes
	//                     (Phase 4c — terminal state)
	//
	// Writes always go to both stores until "postgres-only" lands. A
	// missing or unknown value behaves as "valkey" so deploys without
	// the env var stay safe.
	DedupReadBackend string `env:"DEDUP_READ_BACKEND" envDefault:"valkey"`

	// OpportunityKindsDir is the directory holding the opportunity-kinds YAML
	// registry. Mounted as a ConfigMap in production at this path.
	OpportunityKindsDir string `env:"OPPORTUNITY_KINDS_DIR" envDefault:"/etc/opportunity-kinds"`

	// Canonical-fanout queue subject URLs. The embed + translate
	// handlers are durable Frame Queue subscribers (per the async
	// decision tree: external LLM calls + long-running work →
	// Queue, not Events). Each maps to its own NATS subject under
	// the svc_opportunities_events stream so per-stage backpressure
	// + dead-letter behaviour is independent. Empty defaults to the
	// in-memory driver so local dev / tests work without NATS.
	WorkerEmbedQueueURL     string `env:"WORKER_EMBED_QUEUE_URL"     envDefault:"mem://svc.opportunities.worker.embed.v1"`
	WorkerTranslateQueueURL string `env:"WORKER_TRANSLATE_QUEUE_URL" envDefault:"mem://svc.opportunities.worker.translate.v1"`

	// Pipeline queues — the linear opportunity chain runs on Frame Queue
	// (service-profile idiom: one Name+URI pair per queue; each stage is a
	// native queue.SubscribeWorker that consumes its input queue and
	// qMan.Publish-es to the next by Name). No shared events bus, no
	// catch-all. Each consuming process sets its own URI (same subject,
	// distinct consumer_durable_name); mem:// is the local/test default.
	QueuePipelineIngested       string `env:"QUEUE_PIPELINE_INGESTED_URI"       envDefault:"mem://pipeline_ingested"`
	QueuePipelineIngestedName   string `env:"QUEUE_PIPELINE_INGESTED_NAME"      envDefault:"pipeline_ingested"`
	QueuePipelineNormalized     string `env:"QUEUE_PIPELINE_NORMALIZED_URI"     envDefault:"mem://pipeline_normalized"`
	QueuePipelineNormalizedName string `env:"QUEUE_PIPELINE_NORMALIZED_NAME"    envDefault:"pipeline_normalized"`
	QueuePipelineValidated      string `env:"QUEUE_PIPELINE_VALIDATED_URI"      envDefault:"mem://pipeline_validated"`
	QueuePipelineValidatedName  string `env:"QUEUE_PIPELINE_VALIDATED_NAME"     envDefault:"pipeline_validated"`
	QueuePipelineClustered      string `env:"QUEUE_PIPELINE_CLUSTERED_URI"      envDefault:"mem://pipeline_clustered"`
	QueuePipelineClusteredName  string `env:"QUEUE_PIPELINE_CLUSTERED_NAME"     envDefault:"pipeline_clustered"`
	QueuePipelineCanonical      string `env:"QUEUE_PIPELINE_CANONICAL_URI"      envDefault:"mem://pipeline_canonical"`
	QueuePipelineCanonicalName  string `env:"QUEUE_PIPELINE_CANONICAL_NAME"     envDefault:"pipeline_canonical"`
	QueuePipelinePublished      string `env:"QUEUE_PIPELINE_PUBLISHED_URI"      envDefault:"mem://pipeline_published"`
	QueuePipelinePublishedName  string `env:"QUEUE_PIPELINE_PUBLISHED_NAME"     envDefault:"pipeline_published"`
	QueuePipelineFlagged        string `env:"QUEUE_PIPELINE_FLAGGED_URI"        envDefault:"mem://pipeline_flagged"`
	QueuePipelineFlaggedName    string `env:"QUEUE_PIPELINE_FLAGGED_NAME"       envDefault:"pipeline_flagged"`
	// Derived outputs the embed/translate stages publish (consumed by the
	// materializer + writer sinks).
	QueuePipelineEmbeddings       string `env:"QUEUE_PIPELINE_EMBEDDINGS_URI"     envDefault:"mem://pipeline_embeddings"`
	QueuePipelineEmbeddingsName   string `env:"QUEUE_PIPELINE_EMBEDDINGS_NAME"    envDefault:"pipeline_embeddings"`
	QueuePipelineTranslations     string `env:"QUEUE_PIPELINE_TRANSLATIONS_URI"   envDefault:"mem://pipeline_translations"`
	QueuePipelineTranslationsName string `env:"QUEUE_PIPELINE_TRANSLATIONS_NAME"  envDefault:"pipeline_translations"`

	// StageGroup selects which pipeline handlers this process registers,
	// so the worker can run as independently-scaling consumer groups:
	//   "core"     — normalize, dedup (cluster), canonical
	//   "validate" — validate (llama-bound, fail-open)
	//   "publish"  — publish (R2) + embed + translate
	//   "all"      — every handler on one consumer (legacy monolith; the
	//                default keeps existing single-deployment installs and
	//                the integration tests working unchanged).
	StageGroup string `env:"STAGE_GROUP" envDefault:"all"`

	// ReaperEnabled gates the stuck-variant reaper. Default false: the reaper
	// predates the Frame Queue migration and re-drives via the now-unconsumed
	// events bus, and its time-based "stuck" heuristic misfires under queue
	// backlog (re-driving variants that are merely waiting behind a slow stage),
	// which caused a worker-core CPU storm. The durable queues self-heal via
	// JetStream redelivery, so it is redundant. Re-enable only with a
	// queue-aware, backlog-tolerant rewrite.
	ReaperEnabled bool `env:"REAPER_ENABLED" envDefault:"false"`
}

// Load parses env → Config.
func Load() (Config, error) {
	return fconfig.FromEnv[Config]()
}
