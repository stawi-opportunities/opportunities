// Package config loads apps/worker runtime configuration.
package config

import (
	"time"

	fconfig "github.com/pitabwire/frame/config"
)

// Config for apps/worker. Frame base handles Postgres + pub/sub +
// OTEL; this struct adds Valkey (via Frame's cache framework),
// R2 publish, LLM backends, and translation configuration.
type Config struct {
	fconfig.ConfigurationDefault

	// Valkey URL for Frame's cache framework (backs dedup + cluster
	// snapshot storage). Handed to frame/cache/valkey.New(cache.WithDSN).
	ValkeyURL string `env:"VALKEY_URL,required"`

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

	// Translation target languages. Empty → translator is a no-op.
	// Pipe-separated: "en|sw|fr".
	TranslationLangs []string `env:"TRANSLATION_LANGS" envSeparator:"|"`

	// Minimum validation confidence to mark a variant "validated"
	// (below goes to flagged). Matches the existing handler.
	ValidationMinConfidence float64 `env:"VALIDATION_MIN_CONFIDENCE" envDefault:"0.7"`

	// Validation LLM request timeout.
	ValidationTimeout time.Duration `env:"VALIDATION_TIMEOUT" envDefault:"30s"`

	// OpportunityKindsDir is the directory holding the opportunity-kinds YAML
	// registry. Mounted as a ConfigMap in production at this path.
	OpportunityKindsDir string `env:"OPPORTUNITY_KINDS_DIR" envDefault:"/etc/opportunity-kinds"`
}

// Load parses env → Config.
func Load() (Config, error) {
	return fconfig.FromEnv[Config]()
}
