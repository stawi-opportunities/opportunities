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

	// Iceberg catalog — used by /_admin/kv/rebuild to scan
	// jobs.canonicals_current and repopulate Valkey cluster:* keys.
	IcebergCatalogURI  string `env:"ICEBERG_CATALOG_URI,required"`
	IcebergWarehouse   string `env:"ICEBERG_WAREHOUSE,required"`
	IcebergCatalogName string `env:"ICEBERG_CATALOG_NAME" envDefault:"stawi"`

	// R2 credentials for Iceberg data-file access (shared with publish bucket config).
	R2Endpoint        string `env:"R2_ENDPOINT"          envDefault:""`
	R2Region          string `env:"R2_REGION"            envDefault:"auto"`

	// R2 publish bucket (the live job-detail JSONs, distinct from the
	// event log). pkg/publish creates snapshots here.
	R2PublishAccountID       string `env:"R2_PUBLISH_ACCOUNT_ID,required"`
	R2PublishAccessKeyID     string `env:"R2_PUBLISH_ACCESS_KEY_ID,required"`
	R2PublishSecretAccessKey string `env:"R2_PUBLISH_SECRET_ACCESS_KEY,required"`
	R2PublishBucket          string `env:"R2_PUBLISH_BUCKET,required"`

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
}

// Load parses env → Config.
func Load() (Config, error) {
	return fconfig.FromEnv[Config]()
}
