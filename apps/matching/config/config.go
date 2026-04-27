package config

import (
	fconfig "github.com/pitabwire/frame/config"
)

// CandidatesConfig embeds Frame's ConfigurationDefault and adds
// candidate-service-specific settings.
type CandidatesConfig struct {
	fconfig.ConfigurationDefault

	// Iceberg catalog — Lakekeeper REST endpoint. Lakekeeper owns the
	// metadata DB and storage credentials; this binary only speaks REST.
	// Used by candidatestore.Reader and candidatestore.StaleReader to
	// read candidates.{embeddings,preferences,cv_extracted}_current tables.
	//
	//   ICEBERG_CATALOG_URI   e.g. http://lakekeeper-catalog.lakehouse.svc.cluster.local:8181/catalog
	//   ICEBERG_CATALOG_NAME  local catalog handle, e.g. "stawi"
	//   ICEBERG_WAREHOUSE     logical warehouse registered in Lakekeeper, e.g. "product-opportunities"
	//   ICEBERG_CATALOG_TOKEN optional pre-obtained bearer; leave unset when
	//                         the cluster Lakekeeper runs with auth disabled.
	IcebergCatalogURI   string `env:"ICEBERG_CATALOG_URI,required"`
	IcebergCatalogName  string `env:"ICEBERG_CATALOG_NAME"  envDefault:"stawi"`
	IcebergWarehouse    string `env:"ICEBERG_WAREHOUSE"     envDefault:"product-opportunities"`
	IcebergCatalogToken string `env:"ICEBERG_CATALOG_TOKEN" envDefault:""`

	// Archive R2 (raw CV bytes uploaded by candidates).
	ArchiveR2AccountID       string `env:"ARCHIVE_R2_ACCOUNT_ID"         envDefault:""`
	ArchiveR2AccessKeyID     string `env:"ARCHIVE_R2_ACCESS_KEY_ID"      envDefault:""`
	ArchiveR2SecretAccessKey string `env:"ARCHIVE_R2_SECRET_ACCESS_KEY"  envDefault:""`
	ArchiveR2Bucket          string `env:"ARCHIVE_R2_BUCKET"             envDefault:"product-opportunities-archive"`

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

	// OpportunityKindsDir is the directory holding the opportunity-kinds YAML
	// registry. Mounted as a ConfigMap in production at this path.
	OpportunityKindsDir string `env:"OPPORTUNITY_KINDS_DIR" envDefault:"/etc/opportunity-kinds"`
}
