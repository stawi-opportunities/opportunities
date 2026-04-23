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

	// R2 / S3-compatible event log bucket.
	R2AccountID       string `env:"R2_LOG_ACCOUNT_ID,required"`
	R2AccessKeyID     string `env:"R2_LOG_ACCESS_KEY_ID,required"`
	R2SecretAccessKey string `env:"R2_LOG_SECRET_ACCESS_KEY,required"`
	R2Bucket          string `env:"R2_LOG_BUCKET,required"`
	R2Endpoint        string `env:"R2_LOG_ENDPOINT" envDefault:""`
	R2UsePathStyle    bool   `env:"R2_LOG_PATH_STYLE" envDefault:"false"`

	// Iceberg catalog. Points to the Postgres-backed SQL catalog.
	IcebergCatalogURI string `env:"ICEBERG_CATALOG_URI" envDefault:""`

	// R2 region used when signing requests to the object store.
	R2Region string `env:"R2_LOG_REGION" envDefault:"auto"`

	// Flush thresholds. Whichever trips first forces a flush of the
	// affected partition's buffer. Defaults match the design doc F2
	// freshness target (30 s end-to-end materializer poll → ~60 s
	// serving freshness).
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
	// Memory note (500M-scale): each concurrent compaction goroutine holds at
	// most one Arrow RecordBatch in memory at a time (~1–2 GiB peak per table
	// depending on row-group size). At the default of 4, peak usage is ~4–6 GiB,
	// which fits within the 12 GiB writer pod limit. Reduce to 2 (COMPACT_PARALLELISM=2)
	// if the pod runs on a 4 GiB node or during a memory-constrained period.
	CompactTargetFileSize    int64         `env:"COMPACT_TARGET_FILE_SIZE"     envDefault:"134217728"` // 128 MiB
	CompactMinFileSize       int64         `env:"COMPACT_MIN_FILE_SIZE"        envDefault:"67108864"`  // 64 MiB
	CompactMaxInputPerCommit int           `env:"COMPACT_MAX_INPUT_PER_COMMIT" envDefault:"20"`
	CompactPerTableTimeout   time.Duration `env:"COMPACT_PER_TABLE_TIMEOUT"    envDefault:"30m"`
	CompactParallelism       int           `env:"COMPACT_PARALLELISM"          envDefault:"4"`
}

// Load reads the Config from environment variables.
func Load() (Config, error) {
	return fconfig.FromEnv[Config]()
}
