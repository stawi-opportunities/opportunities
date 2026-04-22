package config

import (
	fconfig "github.com/pitabwire/frame/config"
)

// CandidatesConfig embeds Frame's ConfigurationDefault and adds
// candidate-service-specific settings.
type CandidatesConfig struct {
	fconfig.ConfigurationDefault

	// Iceberg catalog — SQL catalog backed by Postgres + R2.
	// Used by candidatestore.Reader and candidatestore.StaleReader to read
	// candidates.{embeddings,preferences,cv_extracted}_current tables.
	IcebergCatalogURI string `env:"ICEBERG_CATALOG_URI"          envDefault:""`
	IcebergWarehouse  string `env:"ICEBERG_WAREHOUSE"            envDefault:""`
	IcebergCatalogName string `env:"ICEBERG_CATALOG_NAME"        envDefault:"stawi"`

	// R2 event log credentials (shared with Iceberg warehouse and archive).
	R2AccountID       string `env:"R2_ACCOUNT_ID"        envDefault:""`
	R2AccessKeyID     string `env:"R2_ACCESS_KEY_ID"     envDefault:""`
	R2SecretAccessKey string `env:"R2_SECRET_ACCESS_KEY" envDefault:""`
	R2EventLogBucket  string `env:"R2_EVENTLOG_BUCKET"   envDefault:"stawi-jobs-log"`
	R2Endpoint        string `env:"R2_ENDPOINT"          envDefault:""`
	R2Region          string `env:"R2_REGION"            envDefault:"auto"`

	// Archive R2 (raw CV bytes uploaded by candidates).
	ArchiveR2AccountID       string `env:"ARCHIVE_R2_ACCOUNT_ID"         envDefault:""`
	ArchiveR2AccessKeyID     string `env:"ARCHIVE_R2_ACCESS_KEY_ID"      envDefault:""`
	ArchiveR2SecretAccessKey string `env:"ARCHIVE_R2_SECRET_ACCESS_KEY"  envDefault:""`
	ArchiveR2Bucket          string `env:"ARCHIVE_R2_BUCKET"             envDefault:"stawi-jobs-archive"`

	// AI inference back-end (OpenAI-compatible).
	InferenceBaseURL string `env:"INFERENCE_BASE_URL" envDefault:""`
	InferenceAPIKey  string `env:"INFERENCE_API_KEY"  envDefault:""`
	InferenceModel   string `env:"INFERENCE_MODEL"    envDefault:""`

	// Embeddings — optional, graceful no-op when empty.
	EmbeddingBaseURL string `env:"EMBEDDING_BASE_URL" envDefault:""`
	EmbeddingAPIKey  string `env:"EMBEDDING_API_KEY"  envDefault:""`
	EmbeddingModel   string `env:"EMBEDDING_MODEL"    envDefault:""`

	// Reranker (cross-encoder, e.g. BAAI/bge-reranker-v2-m3 via TEI).
	// Matcher falls back to retrieval-order when unset.
	RerankBaseURL string `env:"RERANK_BASE_URL" envDefault:""`
	RerankAPIKey  string `env:"RERANK_API_KEY"  envDefault:""`
	RerankModel   string `env:"RERANK_MODEL"    envDefault:""`

	// Matching-stage feature flags.
	RerankEnabled     bool    `env:"RERANK_ENABLED"      envDefault:"false"`
	RerankSampleRatio float64 `env:"RERANK_SAMPLE_RATIO" envDefault:"1.0"`
	RerankTopK        int     `env:"RERANK_TOP_K"        envDefault:"100"`

	// Manticore Search URL (vector + full-text index for job matching).
	ManticoreURL string `env:"MANTICORE_URL" envDefault:""`
}
