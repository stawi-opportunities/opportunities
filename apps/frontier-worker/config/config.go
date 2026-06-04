// Package config loads apps/frontier-worker runtime configuration.
package config

import (
	fconfig "github.com/pitabwire/frame/config"
)

// Config for apps/frontier-worker. Frame base handles Postgres +
// pub/sub + OTEL. The worker dequeues URLs from url_frontier,
// fetches each, archives the raw HTML to R2, and forwards the
// extracted record into the existing variants pipeline.
type Config struct {
	fconfig.ConfigurationDefault

	// Cloudflare R2 — archive bucket only (frontier-worker doesn't
	// write to the content bucket; the canonical worker owns that).
	R2AccountID       string `env:"R2_ACCOUNT_ID,required"`
	R2AccessKeyID     string `env:"R2_ACCESS_KEY_ID,required"`
	R2SecretAccessKey string `env:"R2_SECRET_ACCESS_KEY,required"`
	R2ArchiveBucket   string `env:"R2_ARCHIVE_BUCKET" envDefault:"product-opportunities-archive"`

	// Pipeline head queue: the frontier-worker publishes VariantIngestedV1
	// here (the worker's normalize stage subscribes). Must match the
	// worker/crawler ingested queue.
	QueuePipelineIngested     string `env:"QUEUE_PIPELINE_INGESTED_URI"  envDefault:"mem://pipeline_ingested"`
	QueuePipelineIngestedName string `env:"QUEUE_PIPELINE_INGESTED_NAME" envDefault:"pipeline_ingested"`

	// AI extractor. Optional — when unset the worker skips the LLM
	// pass and forwards the URL stub with whatever metadata the
	// connector supplied. Same env knobs as apps/crawler so the
	// same secrets work for both.
	InferenceBaseURL string `env:"INFERENCE_BASE_URL"`
	InferenceAPIKey  string `env:"INFERENCE_API_KEY"`
	InferenceModel   string `env:"INFERENCE_MODEL"`
	EmbeddingBaseURL string `env:"EMBEDDING_BASE_URL"`
	EmbeddingAPIKey  string `env:"EMBEDDING_API_KEY"`
	EmbeddingModel   string `env:"EMBEDDING_MODEL"`
	// EmbeddingDimensions pins the embeddings "dimensions" field (Qwen3 MRL);
	// 0 omits it. Must equal EMBEDDING_DIM.
	EmbeddingDimensions int `env:"EMBEDDING_DIMENSIONS" envDefault:"0"`

	// OpportunityKindsDir is the on-disk fallback when R2-backed
	// definitions aren't configured. Mirrors apps/crawler.
	OpportunityKindsDir string `env:"OPPORTUNITY_KINDS_DIR" envDefault:"./definitions/opportunity-kinds"`

	// UserAgent for outbound HTTP. Mirrors crawler.
	UserAgent string `env:"USER_AGENT" envDefault:"StawiBot/1.0 (+https://stawi.org)"`

	// HTTPTimeoutSec bounds the per-URL fetch. Default 20s.
	HTTPTimeoutSec int `env:"HTTP_TIMEOUT_SEC" envDefault:"20"`

	// DequeueBatch caps the URLs claimed per Dequeue call.
	// Default 5 — large enough to amortise the txn cost, small
	// enough that a slow fetch doesn't park other URLs in
	// in_flight for too long.
	DequeueBatch int `env:"DEQUEUE_BATCH" envDefault:"5"`

	// MaxAttempts is the Fail-path retry budget per URL.
	MaxAttempts int `env:"MAX_ATTEMPTS" envDefault:"5"`

	// IdleTickSeconds drives the heartbeat poll cadence — the
	// worker calls Dequeue every N seconds as a fallback in case
	// the NATS wake-up signal misses. Default 5s.
	IdleTickSeconds int `env:"IDLE_TICK_SECONDS" envDefault:"5"`
}

// Load parses env into Config using Frame's env loader.
func Load() (Config, error) {
	return fconfig.FromEnv[Config]()
}
