// Package config defines the PostgreSQL ingestion worker configuration.
package config

import (
	"time"

	fconfig "github.com/pitabwire/frame/v2/config"
)

type Config struct {
	fconfig.ConfigurationDefault

	// ProductDatabaseURL is the Neon (or other) product SoT for opportunities
	// catalog writes. When set, queue claim/ack stay on DATABASE_URL (crawl)
	// and Complete/SetEmbedding/ReconcileSource use this URL. Empty keeps
	// legacy single-database mode.
	ProductDatabaseURL string `env:"PRODUCT_DATABASE_URL" envDefault:""`

	PostgresBatchSize    int           `env:"POSTGRES_BATCH_SIZE" envDefault:"100"`
	PostgresConcurrency  int           `env:"POSTGRES_CONCURRENCY" envDefault:"8"`
	PostgresPollInterval time.Duration `env:"POSTGRES_POLL_INTERVAL" envDefault:"1s"`
	PostgresLease        time.Duration `env:"POSTGRES_LEASE" envDefault:"2m"`
	PostgresMaxAttempts  int           `env:"POSTGRES_MAX_ATTEMPTS" envDefault:"10"`

	// Embeddings — optional. Dimensions must match opportunities.embedding
	// vector(1024). Live path: Complete → Frame Queue (WorkerEmbedQueueURL)
	// → EmbedHandler → SetEmbedding.
	// EmbeddingProvider: nvidia | google | custom (see pkg/extraction).
	EmbeddingProvider   string `env:"EMBEDDING_PROVIDER" envDefault:""`
	EmbeddingBaseURL    string `env:"EMBEDDING_BASE_URL" envDefault:""`
	EmbeddingAPIKey     string `env:"EMBEDDING_API_KEY" envDefault:""`
	EmbeddingModel      string `env:"EMBEDDING_MODEL" envDefault:""`
	EmbeddingDimensions int    `env:"EMBEDDING_DIMENSIONS" envDefault:"1024"`
	// EmbeddingInputType is required by NVIDIA asymmetric E5 NIMs
	// ("passage" for opportunity documents, "query" for search). Empty
	// omits the field for TEI/OpenAI-compat hosts that reject it.
	EmbeddingInputType string `env:"EMBEDDING_INPUT_TYPE" envDefault:""`
	// WorkerEmbedQueueURL publishes SubjectWorkerEmbed jobs. Prefer:
	//   gcppubsub://stawi-opportunities/opportunities-worker-embed
	WorkerEmbedQueueURL string `env:"WORKER_EMBED_QUEUE_URL" envDefault:""`
	// WorkerEmbedSubscribeURL consumes embed jobs. Prefer pull subscription:
	//   gcppubsub://stawi-opportunities/opportunities-worker-embed-pull
	// Empty → reuse WorkerEmbedQueueURL (mem://).
	WorkerEmbedSubscribeURL string `env:"WORKER_EMBED_SUBSCRIBE_URL" envDefault:""`
	// MatchingFanOutQueueURL publishes SubjectOpportunityFanOut. Prefer:
	//   gcppubsub://stawi-opportunities/opportunities-fanout
	MatchingFanOutQueueURL string `env:"MATCHING_FANOUT_QUEUE_URL" envDefault:""`
}

func Load() (Config, error) { return fconfig.FromEnv[Config]() }
