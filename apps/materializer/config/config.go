// Package config loads apps/materializer runtime configuration from
// environment variables. Same pattern as apps/writer/config.
package config

import (
	fconfig "github.com/pitabwire/frame/config"
)

// Config wires Frame defaults (NATS + OTEL) plus materializer-specific
// knobs.
//
// Post-consolidation: the materializer is a Frame topic subscriber
// writing directly to the Postgres opportunities table. Manticore,
// Iceberg catalog URI, R2 credentials, Valkey watermark URL, and poll
// interval are gone — Frame's NATS JetStream consumer group handles
// fan-out, deduplication, and redelivery natively. Consumer lag is
// observable via the NATS Prometheus exporter's
// nats_jetstream_consumer_num_pending metric.
type Config struct {
	fconfig.ConfigurationDefault

	// OpportunityKindsDir is the directory holding the opportunity-kinds YAML
	// registry. Mounted as a ConfigMap in production at this path.
	OpportunityKindsDir string `env:"OPPORTUNITY_KINDS_DIR" envDefault:"/etc/opportunity-kinds"`

	// EmbeddingDim is the native output dimension of the deployed embedding
	// model. It MUST equal the opportunities.embedding vector(N) dimension;
	// the materializer verifies this at boot and refuses to start on a
	// mismatch (a mismatch makes pgvector reject every write, silently
	// dropping all embeddings). 1024 = intfloat/multilingual-e5-large (the
	// deployed model). e5-small would be 384; the value must track whatever
	// EMBEDDING_MODEL is deployed and the schema migration.
	EmbeddingDim int `env:"EMBEDDING_DIM" envDefault:"1024"`
}

// Load parses env → Config.
func Load() (Config, error) {
	return fconfig.FromEnv[Config]()
}
