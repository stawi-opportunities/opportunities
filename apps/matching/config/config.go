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

	// Cloudflare R2 — one account token authorised on all three
	// product-opportunities buckets. Matching uses the archive
	// bucket for raw CV bytes uploaded by candidates; chronicle
	// reads go through the Iceberg catalog (Lakekeeper).
	R2AccountID       string `env:"R2_ACCOUNT_ID"        envDefault:""`
	R2AccessKeyID     string `env:"R2_ACCESS_KEY_ID"     envDefault:""`
	R2SecretAccessKey string `env:"R2_SECRET_ACCESS_KEY" envDefault:""`
	R2Endpoint        string `env:"R2_ENDPOINT"          envDefault:""`
	R2ArchiveBucket   string `env:"R2_ARCHIVE_BUCKET"    envDefault:"product-opportunities-archive"`

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

	// OpportunityKindsDir is the directory holding the opportunity-kinds YAML
	// registry. Mounted as a ConfigMap in production at this path.
	OpportunityKindsDir string `env:"OPPORTUNITY_KINDS_DIR" envDefault:"/etc/opportunity-kinds"`

	// CV-pipeline queue subject URLs. The cv-extract / cv-improve /
	// cv-embed handlers are durable Frame Queue subscribers (per the
	// async decision tree: external LLM calls + long-running work →
	// Queue, not Events). Each maps to its own NATS subject under
	// the svc_opportunities_matching stream so per-stage backpressure
	// + dead-letter behaviour is independent. Empty defaults to the
	// in-memory driver so local dev / tests work without NATS.
	CVExtractQueueURL string `env:"CV_EXTRACT_QUEUE_URL" envDefault:"mem://svc.opportunities.matching.cv.extract.v1"`
	CVImproveQueueURL string `env:"CV_IMPROVE_QUEUE_URL" envDefault:"mem://svc.opportunities.matching.cv.improve.v1"`
	CVEmbedQueueURL   string `env:"CV_EMBED_QUEUE_URL"   envDefault:"mem://svc.opportunities.matching.cv.embed.v1"`

	// PlansURL is embedded into the weekly-jobs-digest event so the
	// notification service's email template doesn't have to assume the
	// host. Defaults to production; preview deploys override via env.
	PlansURL string `env:"PLANS_URL" envDefault:"https://jobs.stawi.org/pricing/"`

	// ValkeyURL is the Valkey/Redis connection URL for the distributed debouncer.
	// When empty (default) the in-memory MemoryDebouncer is used, which is safe
	// for dev/test but does not survive restarts or span multiple replicas.
	ValkeyURL string `env:"VALKEY_URL" envDefault:""`

	// Phase-2 continuous matching pipeline feature flags (spec §5.5).
	// All default to false so the binary is safe to deploy before the
	// pipeline is validated in staging.
	MatchingFanoutEnabled          bool `env:"MATCHING_FANOUT_ENABLED"           envDefault:"false"`
	MatchingCandidateChangeEnabled bool `env:"MATCHING_CANDIDATE_CHANGE_ENABLED" envDefault:"false"`
	MatchingRerankerEnabled        bool `env:"MATCHING_RERANKER_ENABLED"         envDefault:"false"`
	MatchingDLQThreshold           int  `env:"MATCHING_DLQ_THRESHOLD"            envDefault:"5"`
	MatchingDebounceTTLSeconds     int  `env:"MATCHING_DEBOUNCE_TTL_SECONDS"     envDefault:"60"`
	// Phase-4 extension-facing /api/me/* routes (spec §5.5).
	MatchingExtensionEnabled bool `env:"MATCHING_EXTENSION_ENABLED" envDefault:"false"`
}
