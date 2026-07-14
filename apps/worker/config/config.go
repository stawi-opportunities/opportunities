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

	// Embeddings — optional. When EMBEDDING_BASE_URL is empty, rows are
	// stored without vectors (filter search only). Dimensions must match
	// opportunities.embedding vector(1024).
	EmbeddingBaseURL    string `env:"EMBEDDING_BASE_URL" envDefault:""`
	EmbeddingAPIKey     string `env:"EMBEDDING_API_KEY" envDefault:""`
	EmbeddingModel      string `env:"EMBEDDING_MODEL" envDefault:""`
	EmbeddingDimensions int    `env:"EMBEDDING_DIMENSIONS" envDefault:"1024"`
}

func Load() (Config, error) { return fconfig.FromEnv[Config]() }
