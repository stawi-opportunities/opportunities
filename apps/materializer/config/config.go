// Package config loads apps/materializer runtime configuration from
// environment variables. Same pattern as apps/writer/config.
package config

import (
	"time"

	fconfig "github.com/pitabwire/frame/config"
)

// Config wires Frame defaults (NATS + OTEL) plus Manticore URL and
// materializer-specific knobs.
//
// v6.1.0: The materializer is now a Frame topic subscriber. Iceberg catalog
// URI, R2 bucket credentials, Valkey watermark URL, and poll interval are
// removed — Frame's NATS JetStream consumer group handles fan-out,
// deduplication, and redelivery natively. Consumer lag is observable via
// the NATS Prometheus exporter's nats_jetstream_consumer_num_pending metric.
type Config struct {
	fconfig.ConfigurationDefault

	// Manticore HTTP JSON endpoint, e.g. "http://manticore:9308".
	ManticoreURL           string        `env:"MANTICORE_URL,required"`
	ManticoreTimeout       time.Duration `env:"MANTICORE_TIMEOUT" envDefault:"10s"`
	ManticoreBulkBatchSize int           `env:"MANTICORE_BULK_BATCH_SIZE" envDefault:"1000"`
}

// Load parses env → Config.
func Load() (Config, error) {
	return fconfig.FromEnv[Config]()
}
