package telemetry

// iceberg.go — OpenTelemetry metrics for the Iceberg layer.
//
// All instruments are registered under meter name "stawi.jobs.iceberg".
// InitIceberg() is called from Init() and must be invoked once, early in
// main(), before any Iceberg operations run.
//
// Observable-gauge callbacks that touch the catalog (IcebergTableSnapshotsTotal,
// IcebergTableFilesTotal, MaterializerLagSeconds) apply a 30-second TTL cache so
// repeated Prometheus scrapes within the window reuse the same observations
// rather than hammering R2 and the catalog SQL backend.  At 500M-scale the
// catalog holds 14+ tables; loading all of them on every 15-s scrape would be
// tens of catalog round-trips per minute.

import (
	"context"
	"strconv"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/apache/iceberg-go/catalog"
	"github.com/redis/go-redis/v9"
)

// ---------------------------------------------------------------------------
// Instrument declarations
// ---------------------------------------------------------------------------

var (
	icebergMeter = otel.Meter("stawi.jobs.iceberg")

	// Writer commit hot path
	IcebergCommitsTotal    metric.Int64Counter
	IcebergCommitDuration  metric.Float64Histogram
	IcebergCommitRetries   metric.Int64Counter
	IcebergCommitRowsTotal metric.Int64Counter

	// Compaction
	IcebergCompactDuration            metric.Float64Histogram
	IcebergCompactFilesBefore         metric.Int64Counter
	IcebergCompactFilesAfter          metric.Int64Counter
	IcebergCompactBytesRewritten      metric.Int64Counter
	IcebergCompactPartitionsProcessed metric.Int64Counter
	IcebergCompactPartitionsSkipped   metric.Int64Counter
	icebergCompactLastSuccessGauge    metric.Int64ObservableGauge

	// Expire snapshots
	IcebergExpireDuration         metric.Float64Histogram
	IcebergExpireSnapshotsRemoved metric.Int64Counter
	icebergExpireLastSuccessGauge metric.Int64ObservableGauge

	// Table state (observable gauges — updated via catalog-backed callbacks)
	icebergTableSnapshotsGauge metric.Int64ObservableGauge
	icebergTableFilesGauge     metric.Int64ObservableGauge

	// Materializer lag
	materializerLagGauge metric.Float64ObservableGauge

	// Writer buffer
	writerBufferBytesGauge  metric.Int64ObservableGauge
	writerBufferEventsGauge metric.Int64ObservableGauge

	// WriterBufferForceFlushesTotal counts forced flushes of the oldest
	// partition buffer when total buffered bytes exceeds the memory budget.
	// High sustained values indicate the writer pod is memory-constrained.
	WriterBufferForceFlushesTotal metric.Int64Counter
)

// ---------------------------------------------------------------------------
// Last-success timestamps (updated by service code; read by gauge callbacks)
// ---------------------------------------------------------------------------

var (
	compactLastSuccessMu sync.RWMutex
	compactLastSuccess   = map[string]int64{} // table → unix timestamp

	expireLastSuccessMu sync.RWMutex
	expireLastSuccess   = map[string]int64{} // table → unix timestamp
)

// RecordCompactSuccess records a successful compaction for tableName.
// Called by compact.go after a successful per-table run.
func RecordCompactSuccess(tableName string) {
	compactLastSuccessMu.Lock()
	compactLastSuccess[tableName] = time.Now().Unix()
	compactLastSuccessMu.Unlock()
}

// RecordExpireSuccess records a successful snapshot expiry for tableName.
// Called by expire_snapshots.go after a successful per-table run.
func RecordExpireSuccess(tableName string) {
	expireLastSuccessMu.Lock()
	expireLastSuccess[tableName] = time.Now().Unix()
	expireLastSuccessMu.Unlock()
}

// ---------------------------------------------------------------------------
// Observable-gauge registration dependencies
// ---------------------------------------------------------------------------

// IcebergObservablesConfig bundles the external dependencies needed by the
// observable-gauge callbacks.  All fields are optional; missing ones disable
// the corresponding gauge.
type IcebergObservablesConfig struct {
	// Catalog is used by table-state gauges (snapshots, file counts).
	// If nil, those gauges emit no observations.
	Catalog catalog.Catalog

	// TableIdents is the list of table identifiers to observe.  Each entry is
	// a two-element slice: [namespace, table].  Typically this is
	// writer/service.AppendOnlyTables from the writer; pass it in to avoid an
	// import cycle.
	TableIdents [][]string

	// Valkey is used by the materializer-lag gauge to read watermark keys
	// ("mat:snap:<ns>.<table>").  If nil, the lag gauge emits no observations.
	Valkey *redis.Client
}

// BufferStatsFunc is the type of a callback that returns (bytes, events) for
// a named topic.  The writer service registers one of these from buffer.go.
type BufferStatsFunc func(topic string) (bytes int, events int)

var (
	observablesMu     sync.Mutex
	observablesConfig IcebergObservablesConfig
	bufferStatsFunc   BufferStatsFunc // may be nil
	bufferTopics      []string
)

// ---------------------------------------------------------------------------
// 30-second TTL cache for catalog-hitting callbacks
// ---------------------------------------------------------------------------

type cachedObservation struct {
	mu        sync.Mutex
	expiresAt time.Time
	snapshots map[string]int64
	files     map[string]int64
}

var catalogCache = &cachedObservation{}

func (c *cachedObservation) load(
	ctx context.Context,
	cat catalog.Catalog,
	idents [][]string,
) (snapshots, files map[string]int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.expiresAt) {
		return c.snapshots, c.files
	}
	snaps := make(map[string]int64, len(idents))
	fs := make(map[string]int64, len(idents))
	for _, ident := range idents {
		tbl, err := cat.LoadTable(ctx, ident)
		if err != nil {
			continue
		}
		key := ident[0] + "." + ident[1]
		snaps[key] = int64(len(tbl.Metadata().Snapshots()))
		// File count via PlanFiles is expensive at 500M scale.  We report 0
		// here; a future enhancement can wire in a manifest-file count which
		// is cheaper than reading the full manifest.
		fs[key] = 0
	}
	c.snapshots = snaps
	c.files = fs
	c.expiresAt = time.Now().Add(30 * time.Second)
	return c.snapshots, c.files
}

// ---------------------------------------------------------------------------
// InitIceberg — register all Iceberg instruments
// ---------------------------------------------------------------------------

// InitIceberg registers every Iceberg metric instrument with the global OTel
// meter provider.  It is called from Init() and must only be invoked once.
func InitIceberg() error {
	var err error

	// --- Writer commit ---

	IcebergCommitsTotal, err = icebergMeter.Int64Counter(
		"iceberg.commits_total",
		metric.WithDescription("Total Iceberg commit attempts; outcome=ok|retry|fail"),
	)
	if err != nil {
		return err
	}

	IcebergCommitDuration, err = icebergMeter.Float64Histogram(
		"iceberg.commit_duration_seconds",
		metric.WithDescription("Duration of each Iceberg commit attempt"),
	)
	if err != nil {
		return err
	}

	IcebergCommitRetries, err = icebergMeter.Int64Counter(
		"iceberg.commit_retries_total",
		metric.WithDescription("Number of Iceberg commit retry attempts"),
	)
	if err != nil {
		return err
	}

	IcebergCommitRowsTotal, err = icebergMeter.Int64Counter(
		"iceberg.commit_rows_total",
		metric.WithDescription("Total rows committed to Iceberg tables"),
	)
	if err != nil {
		return err
	}

	// --- Compaction ---

	IcebergCompactDuration, err = icebergMeter.Float64Histogram(
		"iceberg.compact_duration_seconds",
		metric.WithDescription("Duration of per-table compaction runs"),
	)
	if err != nil {
		return err
	}

	IcebergCompactFilesBefore, err = icebergMeter.Int64Counter(
		"iceberg.compact_files_before_total",
		metric.WithDescription("Files enumerated before compaction per table"),
	)
	if err != nil {
		return err
	}

	IcebergCompactFilesAfter, err = icebergMeter.Int64Counter(
		"iceberg.compact_files_after_total",
		metric.WithDescription("Files remaining after compaction per table"),
	)
	if err != nil {
		return err
	}

	IcebergCompactBytesRewritten, err = icebergMeter.Int64Counter(
		"iceberg.compact_bytes_rewritten_total",
		metric.WithDescription("Bytes read from small files and rewritten as merged files"),
	)
	if err != nil {
		return err
	}

	IcebergCompactPartitionsProcessed, err = icebergMeter.Int64Counter(
		"iceberg.compact_partitions_processed_total",
		metric.WithDescription("Partitions where compaction committed an Overwrite"),
	)
	if err != nil {
		return err
	}

	IcebergCompactPartitionsSkipped, err = icebergMeter.Int64Counter(
		"iceberg.compact_partitions_skipped_total",
		metric.WithDescription("Partitions skipped during compaction; reason=already_large|single_file|other"),
	)
	if err != nil {
		return err
	}

	icebergCompactLastSuccessGauge, err = icebergMeter.Int64ObservableGauge(
		"iceberg.compact_last_success_unix",
		metric.WithDescription("Unix timestamp of last successful compaction per table; alert on time()-value>7200"),
	)
	if err != nil {
		return err
	}
	_, err = icebergMeter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		compactLastSuccessMu.RLock()
		defer compactLastSuccessMu.RUnlock()
		for tbl, ts := range compactLastSuccess {
			o.ObserveInt64(icebergCompactLastSuccessGauge, ts,
				metric.WithAttributes(attribute.String("table", tbl)))
		}
		return nil
	}, icebergCompactLastSuccessGauge)
	if err != nil {
		return err
	}

	// --- Expire snapshots ---

	IcebergExpireDuration, err = icebergMeter.Float64Histogram(
		"iceberg.expire_duration_seconds",
		metric.WithDescription("Duration of per-table expire-snapshots runs"),
	)
	if err != nil {
		return err
	}

	IcebergExpireSnapshotsRemoved, err = icebergMeter.Int64Counter(
		"iceberg.expire_snapshots_removed_total",
		metric.WithDescription("Snapshots removed by expire-snapshots per table"),
	)
	if err != nil {
		return err
	}

	icebergExpireLastSuccessGauge, err = icebergMeter.Int64ObservableGauge(
		"iceberg.expire_last_success_unix",
		metric.WithDescription("Unix timestamp of last successful expire-snapshots per table"),
	)
	if err != nil {
		return err
	}
	_, err = icebergMeter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		expireLastSuccessMu.RLock()
		defer expireLastSuccessMu.RUnlock()
		for tbl, ts := range expireLastSuccess {
			o.ObserveInt64(icebergExpireLastSuccessGauge, ts,
				metric.WithAttributes(attribute.String("table", tbl)))
		}
		return nil
	}, icebergExpireLastSuccessGauge)
	if err != nil {
		return err
	}

	// --- Table state (catalog-backed, TTL-cached) ---

	icebergTableSnapshotsGauge, err = icebergMeter.Int64ObservableGauge(
		"iceberg.table_snapshots_total",
		metric.WithDescription("Current snapshot count per Iceberg table (30s TTL cache)"),
	)
	if err != nil {
		return err
	}

	icebergTableFilesGauge, err = icebergMeter.Int64ObservableGauge(
		"iceberg.table_files_total",
		metric.WithDescription("Current data file count per Iceberg table (30s TTL cache)"),
	)
	if err != nil {
		return err
	}

	_, err = icebergMeter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		observablesMu.Lock()
		cat := observablesConfig.Catalog
		idents := observablesConfig.TableIdents
		observablesMu.Unlock()
		if cat == nil || len(idents) == 0 {
			return nil
		}
		snaps, files := catalogCache.load(ctx, cat, idents)
		for tbl, n := range snaps {
			o.ObserveInt64(icebergTableSnapshotsGauge, n,
				metric.WithAttributes(attribute.String("table", tbl)))
		}
		for tbl, n := range files {
			o.ObserveInt64(icebergTableFilesGauge, n,
				metric.WithAttributes(attribute.String("table", tbl)))
		}
		return nil
	}, icebergTableSnapshotsGauge, icebergTableFilesGauge)
	if err != nil {
		return err
	}

	// --- Materializer lag ---

	materializerLagGauge, err = icebergMeter.Float64ObservableGauge(
		"materializer_lag_seconds",
		metric.WithDescription("Seconds between current Iceberg snapshot and last materialised snapshot; 0 = caught up"),
	)
	if err != nil {
		return err
	}

	_, err = icebergMeter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		observablesMu.Lock()
		cat := observablesConfig.Catalog
		kv := observablesConfig.Valkey
		idents := observablesConfig.TableIdents
		observablesMu.Unlock()
		if cat == nil || kv == nil || len(idents) == 0 {
			return nil
		}
		// Use the TTL-cached snapshot counts to quickly skip tables without any
		// snapshot data rather than loading every table cold.
		snaps, _ := catalogCache.load(ctx, cat, idents)
		for _, ident := range idents {
			tableKey := ident[0] + "." + ident[1]
			if snaps[tableKey] == 0 {
				continue // nothing committed yet
			}

			// Per-table 1 s timeout so a catalog outage cannot hang a
			// Prometheus scrape for more than 14× 1 s = 14 s total.
			tblCtx, cancel := context.WithTimeout(ctx, time.Second)
			tbl, err := cat.LoadTable(tblCtx, ident)
			cancel()
			if err != nil {
				continue
			}

			currentSnap := tbl.Metadata().CurrentSnapshot()
			if currentSnap == nil {
				continue
			}
			currentMs := currentSnap.TimestampMs

			// Read watermark from Valkey.
			wmKey := "mat:snap:" + tableKey
			v, err := kv.Get(ctx, wmKey).Result()
			if err != nil {
				// Key absent (cold start) — lag equals age of current snapshot.
				lagMs := currentMs - (time.Now().UnixMilli() - currentMs)
				if lagMs < 0 {
					lagMs = 0
				}
				o.ObserveFloat64(materializerLagGauge, float64(currentMs)/1000.0,
					metric.WithAttributes(attribute.String("table", tableKey)))
				continue
			}
			wmSnapID, parseErr := strconv.ParseInt(v, 10, 64)
			if parseErr != nil {
				continue
			}
			// Walk snapshots to find the watermark's timestamp.
			for _, snap := range tbl.Metadata().Snapshots() {
				if snap.SnapshotID == wmSnapID {
					lagMs := currentMs - snap.TimestampMs
					if lagMs < 0 {
						lagMs = 0
					}
					o.ObserveFloat64(materializerLagGauge, float64(lagMs)/1000.0,
						metric.WithAttributes(attribute.String("table", tableKey)))
					break
				}
			}
		}
		return nil
	}, materializerLagGauge)
	if err != nil {
		return err
	}

	// --- Writer buffer ---

	writerBufferBytesGauge, err = icebergMeter.Int64ObservableGauge(
		"writer_buffer_bytes",
		metric.WithDescription("Current bytes buffered in the writer's in-memory partition buffers"),
	)
	if err != nil {
		return err
	}

	writerBufferEventsGauge, err = icebergMeter.Int64ObservableGauge(
		"writer_buffer_events",
		metric.WithDescription("Current events buffered in the writer's in-memory partition buffers"),
	)
	if err != nil {
		return err
	}

	_, err = icebergMeter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		observablesMu.Lock()
		fn := bufferStatsFunc
		topics := bufferTopics
		observablesMu.Unlock()
		if fn == nil {
			return nil
		}
		for _, topic := range topics {
			b, ev := fn(topic)
			o.ObserveInt64(writerBufferBytesGauge, int64(b),
				metric.WithAttributes(attribute.String("topic", topic)))
			o.ObserveInt64(writerBufferEventsGauge, int64(ev),
				metric.WithAttributes(attribute.String("topic", topic)))
		}
		return nil
	}, writerBufferBytesGauge, writerBufferEventsGauge)
	if err != nil {
		return err
	}

	WriterBufferForceFlushesTotal, err = icebergMeter.Int64Counter(
		"writer_buffer_force_flushes_total",
		metric.WithDescription("Times the writer force-flushed the oldest partition buffer due to global memory cap; sustained high rate → pod is memory-constrained"),
	)
	if err != nil {
		return err
	}

	return nil
}

// ---------------------------------------------------------------------------
// Registration helpers called from service main() after catalog load
// ---------------------------------------------------------------------------

// RegisterIcebergObservables wires the catalog (and optionally Valkey) into
// the observable-gauge callbacks.  Call once from each service's main() that
// wants catalog-backed gauges, after the catalog is loaded.
func RegisterIcebergObservables(cfg IcebergObservablesConfig) {
	observablesMu.Lock()
	observablesConfig = cfg
	observablesMu.Unlock()
}

// RegisterBufferStats wires a buffer-stats function into the buffer gauge
// callbacks.  topics is the list of topic names to observe.
func RegisterBufferStats(fn BufferStatsFunc, topics []string) {
	observablesMu.Lock()
	bufferStatsFunc = fn
	bufferTopics = topics
	observablesMu.Unlock()
}

// ---------------------------------------------------------------------------
// Call-site helpers — thin wrappers used by service code to avoid import
// cycles and keep call sites readable
// ---------------------------------------------------------------------------

// RecordCommit records the outcome of one Iceberg commit attempt.
// outcome should be one of: "ok", "retry", "fail".
func RecordCommit(tableName, outcome string, durationSecs float64, rows int64) {
	attrs := metric.WithAttributes(
		attribute.String("table", tableName),
		attribute.String("outcome", outcome),
	)
	IcebergCommitsTotal.Add(context.Background(), 1, attrs)
	IcebergCommitDuration.Record(context.Background(), durationSecs, attrs)
	if rows > 0 {
		IcebergCommitRowsTotal.Add(context.Background(), rows,
			metric.WithAttributes(attribute.String("table", tableName)))
	}
}

// RecordCommitRetry records a single retry attempt on a table.
func RecordCommitRetry(tableName string) {
	IcebergCommitRetries.Add(context.Background(), 1,
		metric.WithAttributes(attribute.String("table", tableName)))
}

// RecordCompactDuration records the duration of a per-table compact run.
func RecordCompactDuration(tableName string, secs float64) {
	IcebergCompactDuration.Record(context.Background(), secs,
		metric.WithAttributes(attribute.String("table", tableName)))
}

// RecordCompactFileCounts records file-count before/after for a table.
func RecordCompactFileCounts(tableName string, before, after int) {
	attrs := metric.WithAttributes(attribute.String("table", tableName))
	IcebergCompactFilesBefore.Add(context.Background(), int64(before), attrs)
	IcebergCompactFilesAfter.Add(context.Background(), int64(after), attrs)
}

// RecordCompactBytesRewritten increments the bytes-rewritten counter.
func RecordCompactBytesRewritten(tableName string, bytes int64) {
	IcebergCompactBytesRewritten.Add(context.Background(), bytes,
		metric.WithAttributes(attribute.String("table", tableName)))
}

// RecordCompactPartitionProcessed increments the processed-partitions counter.
func RecordCompactPartitionProcessed(tableName string) {
	IcebergCompactPartitionsProcessed.Add(context.Background(), 1,
		metric.WithAttributes(attribute.String("table", tableName)))
}

// RecordCompactPartitionSkipped increments the skipped-partitions counter.
// reason should be one of: "already_large", "single_file", "other".
func RecordCompactPartitionSkipped(tableName, reason string) {
	IcebergCompactPartitionsSkipped.Add(context.Background(), 1,
		metric.WithAttributes(
			attribute.String("table", tableName),
			attribute.String("reason", reason),
		))
}

// RecordExpire records the outcome of one expire-snapshots run for a table.
func RecordExpire(tableName string, durationSecs float64, removed int) {
	attrs := metric.WithAttributes(attribute.String("table", tableName))
	IcebergExpireDuration.Record(context.Background(), durationSecs, attrs)
	IcebergExpireSnapshotsRemoved.Add(context.Background(), int64(removed), attrs)
}
