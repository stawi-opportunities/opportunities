// Package config loads apps/materializer runtime configuration from
// environment variables. Same pattern as apps/writer/config.
package config

import (
	"time"

	fconfig "github.com/pitabwire/frame/config"
)

// Config wires Frame defaults (NATS + OTEL) plus Manticore URL,
// Iceberg catalog, Valkey, and materializer-specific knobs.
//
// v6.0.0: DATABASE_URL / Postgres is no longer required by the
// materializer; watermarks moved to Valkey. The frame.WithDatastore()
// call in main.go is removed accordingly.
type Config struct {
	fconfig.ConfigurationDefault

	// R2 / S3-compatible event log bucket (Parquet file reader).
	R2AccountID       string `env:"R2_LOG_ACCOUNT_ID,required"`
	R2AccessKeyID     string `env:"R2_LOG_ACCESS_KEY_ID,required"`
	R2SecretAccessKey string `env:"R2_LOG_SECRET_ACCESS_KEY,required"`
	R2Bucket          string `env:"R2_LOG_BUCKET,required"`
	R2Endpoint        string `env:"R2_LOG_ENDPOINT" envDefault:""`
	R2UsePathStyle    bool   `env:"R2_LOG_PATH_STYLE" envDefault:"false"`

	// R2 region used for Iceberg catalog S3 signing.
	R2Region string `env:"R2_LOG_REGION" envDefault:"auto"`

	// Iceberg SQL catalog URI (postgres://...).
	IcebergCatalogURI string `env:"ICEBERG_CATALOG_URI,required"`

	// Valkey connection URL, e.g. "redis://valkey:6379/0".
	ValkeyURL string `env:"VALKEY_URL,required"`

	// Manticore HTTP JSON endpoint, e.g. "http://manticore:9308".
	ManticoreURL          string        `env:"MANTICORE_URL,required"`
	ManticoreTimeout      time.Duration `env:"MANTICORE_TIMEOUT" envDefault:"10s"`
	ManticoreBulkBatchSize int          `env:"MANTICORE_BULK_BATCH_SIZE" envDefault:"1000"`

	// Polling cadence.
	PollInterval time.Duration `env:"MATERIALIZER_POLL_INTERVAL" envDefault:"15s"`
}

// Load parses env → Config.
func Load() (Config, error) {
	return fconfig.FromEnv[Config]()
}
