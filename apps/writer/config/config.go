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

	// Cloudflare R2 — one account token authorised on all three
	// product-opportunities buckets. The writer talks to the
	// chronicle bucket: bootstrap-iceberg passes these credentials
	// to Lakekeeper at warehouse-registration time, and the runtime
	// writer commits parquet via the Iceberg REST catalog.
	R2AccountID       string `env:"R2_ACCOUNT_ID,required"`
	R2AccessKeyID     string `env:"R2_ACCESS_KEY_ID,required"`
	R2SecretAccessKey string `env:"R2_SECRET_ACCESS_KEY,required"`
	R2Bucket          string `env:"R2_CHRONICLE_BUCKET,required"`
	R2Endpoint        string `env:"R2_ENDPOINT" envDefault:""`
	R2UsePathStyle    bool   `env:"R2_PATH_STYLE" envDefault:"false"`

	// Iceberg catalog — Cloudflare R2 Data Catalog REST endpoint.
	// R2 Data Catalog is a managed Iceberg REST catalog built into the
	// R2 bucket; no separate catalog service to run.
	//
	//   ICEBERG_CATALOG_URI   the catalog URI returned by `wrangler r2 bucket catalog enable`
	//   ICEBERG_CATALOG_NAME  local catalog handle for iceberg-go logs
	//   ICEBERG_WAREHOUSE     warehouse name returned alongside the catalog URI
	//   ICEBERG_CATALOG_TOKEN Cloudflare API token with R2 Data Catalog + R2 Storage permissions
	IcebergCatalogURI   string `env:"ICEBERG_CATALOG_URI,required"`
	IcebergCatalogName  string `env:"ICEBERG_CATALOG_NAME"  envDefault:"stawi"`
	IcebergWarehouse    string `env:"ICEBERG_WAREHOUSE"     envDefault:""`
	IcebergCatalogToken string `env:"ICEBERG_CATALOG_TOKEN" envDefault:""`

	// Cloudflare API token used by bootstrap to enable R2 Data Catalog
	// on the bucket. Same token can serve as ICEBERG_CATALOG_TOKEN if
	// it has both R2 Data Catalog and R2 Storage permissions.
	CloudflareAPIToken string `env:"CLOUDFLARE_API_TOKEN" envDefault:""`

	// R2 region used when signing requests to the object store.
	R2Region string `env:"R2_REGION" envDefault:"auto"`

	// Flush thresholds. Whichever trips first forces a flush of the
	// affected partition's buffer. Defaults match the design doc F2
	// freshness target (30 s end-to-end materializer poll → ~60 s
	// serving freshness).
	//
	// Dynamic adaptation: the writer Buffer also enforces a global memory cap
	// (30% of pod memory via memconfig) across all open partition buffers.
	// That cap is applied at runtime and does not require config changes.
	// These thresholds are per-partition limits; the global cap triggers
	// force-flush of the oldest partition when total buffered bytes exceed the
	// budget — regardless of per-partition thresholds.
	//
	// Startup validation (informational): if FlushMaxBytes × EstimatedConcurrentPartitions
	// exceeds the 30% budget, a warning is logged by the Buffer constructor.
	// Default of 64 MiB × ~5 concurrent partitions = 320 MiB fits within the
	// 30% budget of a 1 GiB pod (307 MiB). On larger pods the global cap scales
	// up automatically — no config change needed.
	FlushMaxEvents   int           `env:"WRITER_FLUSH_MAX_EVENTS" envDefault:"10000"`
	FlushMaxBytes    int           `env:"WRITER_FLUSH_MAX_BYTES"  envDefault:"67108864"` // 64 MiB
	FlushMaxInterval time.Duration `env:"WRITER_FLUSH_MAX_INTERVAL" envDefault:"30s"`

	// Snapshot retention knobs used by the /_admin/expire-snapshots endpoint.
	// Snapshots older than SnapshotRetentionDays are eligible for expiry, but
	// at least MinSnapshotsToKeep are always retained per table regardless of age.
	SnapshotRetentionDays int `env:"SNAPSHOT_RETENTION_DAYS" envDefault:"14"`
	MinSnapshotsToKeep    int `env:"MIN_SNAPSHOTS_TO_KEEP"   envDefault:"100"`

	// Compaction knobs used by the /_admin/compact endpoint.
	//
	// CompactTargetFileSize is the desired output Parquet file size.
	// CompactMinFileSize is the threshold below which a file is "small"
	// and eligible for merging (default TargetFileSize/2 = 64 MiB).
	// CompactMaxInputPerCommit caps how many small files are merged per
	// per-partition Overwrite transaction; keeps individual commits bounded.
	// CompactPerTableTimeout limits each table's compaction goroutine.
	// CompactParallelism fans out across tables (same as Parallelism for expire).
	//
	// Memory note: each concurrent compaction goroutine peaks at ~1.5 GiB.
	// When CompactParallelism is 0 (the default), the parallelism is computed
	// adaptively at runtime: (50% of pod memory) / 1.5 GiB, minimum 1.
	// This means a 1 GiB pod runs 1 partition at a time (slower, but never OOM),
	// while a 12 GiB pod runs up to 4 in parallel. Set COMPACT_PARALLELISM to a
	// positive integer to override with a ceiling.
	CompactTargetFileSize    int64         `env:"COMPACT_TARGET_FILE_SIZE"     envDefault:"134217728"` // 128 MiB
	CompactMinFileSize       int64         `env:"COMPACT_MIN_FILE_SIZE"        envDefault:"67108864"`  // 64 MiB
	CompactMaxInputPerCommit int           `env:"COMPACT_MAX_INPUT_PER_COMMIT" envDefault:"20"`
	CompactPerTableTimeout   time.Duration `env:"COMPACT_PER_TABLE_TIMEOUT"    envDefault:"30m"`
	CompactParallelism       int           `env:"COMPACT_PARALLELISM"          envDefault:"0"`

	// OpportunityKindsDir is the directory holding the opportunity-kinds YAML
	// registry. Mounted as a ConfigMap in production at this path.
	OpportunityKindsDir string `env:"OPPORTUNITY_KINDS_DIR" envDefault:"/etc/opportunity-kinds"`

	// Pipeline-stage Frame Queues (service-profile idiom). The writer is a
	// fan-out durable consumer of each — own consumer_durable_name on the same
	// subject the in-pipeline consumer (worker / next stage) drains. Name+URI
	// must match the worker's QUEUE_PIPELINE_* so the writer sees every stage
	// event for Iceberg archival; mem:// is the local/test default. One pair
	// per topic in service.PipelineQueueTopics().
	QueuePipelineIngested       string `env:"QUEUE_PIPELINE_INGESTED_URI"     envDefault:"mem://pipeline_ingested"`
	QueuePipelineIngestedName   string `env:"QUEUE_PIPELINE_INGESTED_NAME"    envDefault:"pipeline_ingested"`
	QueuePipelineNormalized     string `env:"QUEUE_PIPELINE_NORMALIZED_URI"   envDefault:"mem://pipeline_normalized"`
	QueuePipelineNormalizedName string `env:"QUEUE_PIPELINE_NORMALIZED_NAME"  envDefault:"pipeline_normalized"`
	QueuePipelineValidated      string `env:"QUEUE_PIPELINE_VALIDATED_URI"    envDefault:"mem://pipeline_validated"`
	QueuePipelineValidatedName  string `env:"QUEUE_PIPELINE_VALIDATED_NAME"   envDefault:"pipeline_validated"`
	QueuePipelineFlagged        string `env:"QUEUE_PIPELINE_FLAGGED_URI"      envDefault:"mem://pipeline_flagged"`
	QueuePipelineFlaggedName    string `env:"QUEUE_PIPELINE_FLAGGED_NAME"     envDefault:"pipeline_flagged"`
	QueuePipelineClustered      string `env:"QUEUE_PIPELINE_CLUSTERED_URI"    envDefault:"mem://pipeline_clustered"`
	QueuePipelineClusteredName  string `env:"QUEUE_PIPELINE_CLUSTERED_NAME"   envDefault:"pipeline_clustered"`
	QueuePipelineEmbeddings     string `env:"QUEUE_PIPELINE_EMBEDDINGS_URI"   envDefault:"mem://pipeline_embeddings"`
	QueuePipelineEmbeddingsName string `env:"QUEUE_PIPELINE_EMBEDDINGS_NAME"  envDefault:"pipeline_embeddings"`
	QueuePipelinePublished      string `env:"QUEUE_PIPELINE_PUBLISHED_URI"    envDefault:"mem://pipeline_published"`
	QueuePipelinePublishedName  string `env:"QUEUE_PIPELINE_PUBLISHED_NAME"   envDefault:"pipeline_published"`
}

// Load reads the Config from environment variables.
func Load() (Config, error) {
	return fconfig.FromEnv[Config]()
}
