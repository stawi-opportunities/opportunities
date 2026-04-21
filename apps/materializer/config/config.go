// Package config loads apps/materializer runtime configuration from
// environment variables. Same pattern as apps/writer/config.
package config

import (
	"time"

	fconfig "github.com/pitabwire/frame/config"
)

// Config wires Frame defaults (DB + NATS + OTEL) plus Manticore URL
// and materializer-specific knobs.
type Config struct {
	fconfig.ConfigurationDefault

	// R2 / S3-compatible event log bucket (reader side).
	R2AccountID       string `env:"R2_LOG_ACCOUNT_ID,required"`
	R2AccessKeyID     string `env:"R2_LOG_ACCESS_KEY_ID,required"`
	R2SecretAccessKey string `env:"R2_LOG_SECRET_ACCESS_KEY,required"`
	R2Bucket          string `env:"R2_LOG_BUCKET,required"`
	R2Endpoint        string `env:"R2_LOG_ENDPOINT" envDefault:""`
	R2UsePathStyle    bool   `env:"R2_LOG_PATH_STYLE" envDefault:"false"`

	// Manticore HTTP JSON endpoint, e.g. "http://manticore:9308".
	ManticoreURL     string        `env:"MANTICORE_URL,required"`
	ManticoreTimeout time.Duration `env:"MANTICORE_TIMEOUT" envDefault:"10s"`

	// Polling cadence + batch cap.
	PollInterval  time.Duration `env:"MATERIALIZER_POLL_INTERVAL" envDefault:"15s"`
	ListBatchSize int32         `env:"MATERIALIZER_LIST_BATCH"   envDefault:"100"`

	// Partition prefixes to track. Pipe-separated list; Phase 2 covers
	// canonicals + embeddings. Later phases extend this.
	Prefixes []string `env:"MATERIALIZER_PREFIXES" envSeparator:"|" envDefault:"canonicals/|embeddings/"`
}

// Load parses env → Config.
func Load() (Config, error) {
	return fconfig.FromEnv[Config]()
}
