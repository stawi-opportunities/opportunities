// Package config loads apps/writer runtime configuration from
// environment variables. Mirrors the convention used by apps/crawler
// and apps/api — frame/config.FromEnv with sensible defaults.
package config

import (
	"time"

	fconfig "github.com/pitabwire/frame/config"
)

// Config is the full apps/writer config: Frame base fields (which
// includes Postgres, NATS, OTEL wiring) plus writer-specific thresholds.
type Config struct {
	fconfig.ConfigurationDefault

	// R2 / S3-compatible event log bucket.
	R2AccountID       string `env:"R2_LOG_ACCOUNT_ID,required"`
	R2AccessKeyID     string `env:"R2_LOG_ACCESS_KEY_ID,required"`
	R2SecretAccessKey string `env:"R2_LOG_SECRET_ACCESS_KEY,required"`
	R2Bucket          string `env:"R2_LOG_BUCKET,required"`
	R2Endpoint        string `env:"R2_LOG_ENDPOINT" envDefault:""`
	R2UsePathStyle    bool   `env:"R2_LOG_PATH_STYLE" envDefault:"false"`

	// Flush thresholds. Whichever trips first forces a flush of the
	// affected partition's buffer. Defaults match the design doc F2
	// freshness target (30 s end-to-end materializer poll → ~60 s
	// serving freshness).
	FlushMaxEvents   int           `env:"WRITER_FLUSH_MAX_EVENTS" envDefault:"10000"`
	FlushMaxBytes    int           `env:"WRITER_FLUSH_MAX_BYTES"  envDefault:"67108864"` // 64 MiB
	FlushMaxInterval time.Duration `env:"WRITER_FLUSH_MAX_INTERVAL" envDefault:"30s"`
}

// Load reads the Config from environment variables.
func Load() (Config, error) {
	return fconfig.FromEnv[Config]()
}
