// Package config defines the PostgreSQL ingestion worker configuration.
package config

import (
	"time"

	fconfig "github.com/pitabwire/frame/v2/config"
)

type Config struct {
	fconfig.ConfigurationDefault

	PostgresBatchSize    int           `env:"POSTGRES_BATCH_SIZE" envDefault:"100"`
	PostgresConcurrency  int           `env:"POSTGRES_CONCURRENCY" envDefault:"8"`
	PostgresPollInterval time.Duration `env:"POSTGRES_POLL_INTERVAL" envDefault:"1s"`
	PostgresLease        time.Duration `env:"POSTGRES_LEASE" envDefault:"2m"`
	PostgresMaxAttempts  int           `env:"POSTGRES_MAX_ATTEMPTS" envDefault:"10"`

	// Embeddings — optional. Dimensions must match opportunities.embedding
	// vector(1024). Live path: Complete → Frame Queue (WorkerEmbedQueueURL)
	// → EmbedHandler → SetEmbedding.
	EmbeddingBaseURL    string `env:"EMBEDDING_BASE_URL" envDefault:""`
	EmbeddingAPIKey     string `env:"EMBEDDING_API_KEY" envDefault:""`
	EmbeddingModel      string `env:"EMBEDDING_MODEL" envDefault:""`
	EmbeddingDimensions int    `env:"EMBEDDING_DIMENSIONS" envDefault:"1024"`
	// EmbeddingInputType is required by NVIDIA asymmetric E5 NIMs
	// ("passage" for opportunity documents, "query" for search). Empty
	// omits the field for TEI/OpenAI-compat hosts that reject it.
	EmbeddingInputType string `env:"EMBEDDING_INPUT_TYPE" envDefault:""`
	// WorkerEmbedQueueURL is the Frame Queue DSN for SubjectWorkerEmbed
	// (svc.opportunities.worker.embed.v1). Required together with
	// EMBEDDING_BASE_URL to enable opportunity vectors.
	WorkerEmbedQueueURL string `env:"WORKER_EMBED_QUEUE_URL" envDefault:""`
}

func Load() (Config, error) { return fconfig.FromEnv[Config]() }
