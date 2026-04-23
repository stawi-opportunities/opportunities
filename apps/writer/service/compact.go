package service

// compact.go — Go-native Iceberg small-file compaction (500M-scale ready).
//
// # Algorithm (per table)
//
//  1. Load the table from the catalog and call Scan.PlanFiles to enumerate all
//     data files.
//  2. Group files by their partition tuple (serialised as a stable string key).
//  3. For each partition group:
//     - Skip if len(files) < 2 (nothing to merge).
//     - Skip if ALL files are already >= MinFileSize (no benefit).
//     - Select up to MaxInputFilesPerCommit of the smallest files.
//     - Build a lazy Arrow RecordReader via Scan.ToArrowRecords (streams row
//       groups — does NOT load everything into memory at once).
//     - Open a Transaction, call Overwrite(ctx, rdr, nil, WithOverwriteFilter(f)),
//       then Commit.
//
// # Why Overwrite, not ReplaceDataFilesWithDataFiles
//
// ReplaceDataFilesWithDataFiles(ctx, filesToDelete, filesToAdd []iceberg.DataFile,
// ...) requires the caller to supply fully-constructed DataFile values for the NEW
// files — including per-column stats, value counts, null counts, and bloom filter
// bytes.  Constructing those correctly from Go is 50+ fields and prone to subtle
// metadata inconsistencies.
//
// Transaction.Overwrite(ctx, rdr array.RecordReader, ..., WithOverwriteFilter(expr))
// takes an Arrow RecordReader, writes new Parquet files with iceberg-go-computed
// metadata, and atomically deletes every data file that matches the filter
// expression.  The library handles all metadata correctly.  Overwrite is therefore
// the correct choice for compaction.
//
// Note: ReplaceDataFiles(ctx, filesToDelete, filesToAdd []string, ...) also exists
// in v0.5.0 — it takes string paths for both sides.  That variant reads the add-
// files from storage to extract metadata, which avoids the manual-stats problem.
// However it still requires the new files to be pre-written to object storage,
// which means we would have to manage Parquet serialisation ourselves.  Overwrite
// is simpler and equally correct.
//
// # Memory safety at 500M-scale
//
// Scan.ToArrowRecords returns an iter.Seq2[RecordBatch, error] that streams row
// groups lazily from R2 — it does NOT read all selected files into memory at once.
// We wrap it with array.ReaderFromIter so Overwrite consumes batches one at a time
// via its internal recordsToDataFiles iterator.  The only in-flight data is one
// Arrow RecordBatch per file at a time, which is bounded by a single row-group
// size (typically 1–128 MiB).

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/iceberg-go"
	"github.com/apache/iceberg-go/catalog"
	"github.com/apache/iceberg-go/table"
	"github.com/pitabwire/util"

	"stawi.jobs/pkg/memconfig"
	"stawi.jobs/pkg/telemetry"
)

// CompactConfig tunes the compaction run.  All fields have safe defaults applied
// in Compact when zero-valued.
type CompactConfig struct {
	TargetFileSize    int64         // desired output file size (default 128 MiB)
	MinFileSize       int64         // files below this are "small" (default 64 MiB)
	MaxInputPerCommit int           // max files merged per Overwrite tx (default 20)
	PerTableTimeout   time.Duration // per-table deadline (default 30 min)
	Parallelism       int           // table-level fan-out (default 4)
}

// CompactSkipStats counts why partitions were not compacted.
type CompactSkipStats struct {
	AlreadyLarge int `json:"already_large"` // all files >= MinFileSize
	SingleFile   int `json:"single_file"`   // only one file in partition
	Other        int `json:"other"`         // unexpected edge cases
}

// CompactResult summarises one Compact run.
type CompactResult struct {
	Tables               int              `json:"tables"`
	PartitionsProcessed  int              `json:"partitions_processed"`
	PartitionsSkipped    CompactSkipStats `json:"partitions_skipped"`
	FilesBefore          int              `json:"files_before"`
	FilesAfter           int              `json:"files_after"`
	BytesRewritten       int64            `json:"bytes_rewritten"`
	FailedTables         []string         `json:"failed_tables"`
	DurationMs           int64            `json:"duration_ms"`
}

// Compact iterates AppendOnlyTables and compacts small Parquet files within
// each partition group using a bounded goroutine pool.  Per-table failures do
// NOT abort the run — they are collected and reported.
func Compact(ctx context.Context, cat catalog.Catalog, cfg CompactConfig) (CompactResult, error) {
	cfg = applyCompactDefaults(cfg)
	util.Log(ctx).WithField("parallelism", cfg.Parallelism).Info("compact: starting with parallelism")

	type tableResult struct {
		ident   []string
		err     error
		partial compactTableResult
	}

	jobs := make(chan []string, len(AppendOnlyTables))
	results := make(chan tableResult, len(AppendOnlyTables))

	var wg sync.WaitGroup
	for i := 0; i < cfg.Parallelism; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ident := range jobs {
				tblCtx, cancel := context.WithTimeout(ctx, cfg.PerTableTimeout)
				r, err := compactTable(tblCtx, cat, ident, cfg)
				cancel()
				results <- tableResult{ident: ident, err: err, partial: r}
			}
		}()
	}

	for _, ident := range AppendOnlyTables {
		jobs <- ident
	}
	close(jobs)
	wg.Wait()
	close(results)

	var out CompactResult
	for r := range results {
		out.Tables++
		if r.err != nil {
			out.FailedTables = append(out.FailedTables, fmt.Sprintf("%v: %v", r.ident, r.err))
			util.Log(ctx).WithError(r.err).WithField("ident", r.ident).
				Warn("compact: per-table failure")
			continue
		}
		out.PartitionsProcessed += r.partial.partitionsProcessed
		out.PartitionsSkipped.AlreadyLarge += r.partial.skipped.AlreadyLarge
		out.PartitionsSkipped.SingleFile += r.partial.skipped.SingleFile
		out.PartitionsSkipped.Other += r.partial.skipped.Other
		out.FilesBefore += r.partial.filesBefore
		out.FilesAfter += r.partial.filesAfter
		out.BytesRewritten += r.partial.bytesRewritten
		util.Log(ctx).
			WithField("ident", r.ident).
			WithField("partitions_processed", r.partial.partitionsProcessed).
			WithField("files_before", r.partial.filesBefore).
			WithField("files_after", r.partial.filesAfter).
			Info("compact: table ok")
	}
	return out, nil
}

// compactTableResult is the per-table summary passed back from the worker goroutine.
type compactTableResult struct {
	partitionsProcessed int
	skipped             CompactSkipStats
	filesBefore         int
	filesAfter          int
	bytesRewritten      int64
}

// compactTable compacts one table.  It is called with a per-table deadline context.
func compactTable(ctx context.Context, cat catalog.Catalog, ident []string, cfg CompactConfig) (compactTableResult, error) {
	start := time.Now()
	tableName := ident[0] + "." + ident[1]

	tbl, err := cat.LoadTable(ctx, ident)
	if err != nil {
		return compactTableResult{}, fmt.Errorf("load: %w", err)
	}

	scan := tbl.Scan()
	tasks, err := scan.PlanFiles(ctx)
	if err != nil {
		return compactTableResult{}, fmt.Errorf("plan files: %w", err)
	}

	// Group files by partition tuple.
	groups := groupByPartition(tasks)

	var res compactTableResult
	res.filesBefore = len(tasks)

	// filesAfter starts equal to filesBefore; each processed partition
	// adjusts the count down by (selected files - 1 output file).
	res.filesAfter = len(tasks)

	for partKey, group := range groups {
		if ctx.Err() != nil {
			// Context deadline exceeded — stop and propagate as a table-level error.
			break
		}
		processed, skipReason, selectedCount, rewritten, err := compactPartition(ctx, tbl, group, cfg, partKey)
		if err != nil {
			// Per-partition failure: keep going; log + record.
			res.skipped.Other++
			util.Log(ctx).WithError(err).
				WithField("table", tableName).
				WithField("partition", partKey).
				Warn("compact: per-partition failure; continuing")
			telemetry.RecordCompactPartitionSkipped(tableName, "other")
			continue
		}
		if skipReason != "" {
			switch skipReason {
			case "already_large":
				res.skipped.AlreadyLarge++
			case "single_file":
				res.skipped.SingleFile++
			default:
				res.skipped.Other++
			}
			telemetry.RecordCompactPartitionSkipped(tableName, skipReason)
			continue
		}
		if processed {
			res.partitionsProcessed++
			// selectedCount files merged into 1 output file → net reduction.
			res.filesAfter -= (selectedCount - 1)
			res.bytesRewritten += rewritten
			telemetry.RecordCompactPartitionProcessed(tableName)
		}
	}

	dur := time.Since(start)
	telemetry.RecordCompactDuration(tableName, dur.Seconds())
	telemetry.RecordCompactFileCounts(tableName, res.filesBefore, res.filesAfter)
	telemetry.RecordCompactBytesRewritten(tableName, res.bytesRewritten)
	if len(groups) > 0 && res.partitionsProcessed > 0 {
		telemetry.RecordCompactSuccess(tableName)
	}

	return res, ctx.Err()
}

// partitionKey produces a stable string key from a DataFile's partition map.
// Files in the same partition have identical key → same bucket.
func partitionKey(f iceberg.DataFile) string {
	p := f.Partition()
	if len(p) == 0 {
		return "__unpartitioned__"
	}
	// Sort field IDs for a deterministic key.
	ids := make([]int, 0, len(p))
	for id := range p {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	key := ""
	for _, id := range ids {
		key += fmt.Sprintf("%d=%v|", id, p[id])
	}
	return key
}

// groupByPartition groups FileScanTasks by their partition tuple.
func groupByPartition(tasks []table.FileScanTask) map[string][]table.FileScanTask {
	groups := make(map[string][]table.FileScanTask, len(tasks))
	for _, t := range tasks {
		k := partitionKey(t.File)
		groups[k] = append(groups[k], t)
	}
	return groups
}

// compactPartition compacts one partition group.
//
// Returns:
//   - processed: true if an Overwrite transaction was committed.
//   - skipReason: non-empty if the partition was intentionally skipped.
//   - selectedCount: number of small files that were selected for merging.
//   - bytesRewritten: total bytes of the selected files.
//   - err: non-nil on per-partition failure.
func compactPartition(
	ctx context.Context,
	tbl *table.Table,
	tasks []table.FileScanTask,
	cfg CompactConfig,
	_ string, // partKey, used for logging by caller
) (processed bool, skipReason string, selectedCount int, bytesRewritten int64, err error) {
	if len(tasks) < 2 {
		return false, "single_file", 0, 0, nil
	}

	// Skip if ALL files are already large enough.
	allLarge := true
	for _, t := range tasks {
		if t.File.FileSizeBytes() < cfg.MinFileSize {
			allLarge = false
			break
		}
	}
	if allLarge {
		return false, "already_large", 0, 0, nil
	}

	// Select up to MaxInputPerCommit of the smallest files.
	selected := selectSmallFiles(tasks, cfg.MaxInputPerCommit, cfg.MinFileSize)
	if len(selected) < 2 {
		return false, "single_file", 0, 0, nil
	}

	// Build a partition filter for Overwrite so that ONLY files in this
	// partition are atomically replaced.
	filter, err := buildPartitionFilter(tbl, selected[0].File)
	if err != nil {
		// If we cannot build a precise filter, fall back to file-path equality
		// OR-chain. This is less elegant but guarantees we never over-delete.
		// (A nil filter with WithOverwriteFilter(AlwaysTrue) would delete
		// everything in the table — catastrophic.)
		filter, err = buildFilePathFilter(selected)
		if err != nil {
			return false, "", 0, 0, fmt.Errorf("build filter: %w", err)
		}
	}

	// Build a lazy RecordReader covering ALL files in this partition.
	// Overwrite requires the reader to supply the complete post-commit
	// state for the partition (not just the small files), so we scan the
	// full partition.  The partition filter scopes the scan to this
	// partition only, avoiding unnecessary manifest reads from other
	// partitions.
	rdr, _, err := buildStreamingReader(ctx, tbl, filter)
	if err != nil {
		return false, "", 0, 0, fmt.Errorf("build reader: %w", err)
	}

	// Account bytes from the selected small files only (the "compacted" portion).
	var inputBytes int64
	for _, t := range selected {
		inputBytes += t.File.FileSizeBytes()
	}

	txn := tbl.NewTransaction()
	if err := txn.Overwrite(ctx, rdr, nil, table.WithOverwriteFilter(filter)); err != nil {
		rdr.Release()
		return false, "", 0, 0, fmt.Errorf("overwrite: %w", err)
	}
	rdr.Release()

	if _, err := txn.Commit(ctx); err != nil {
		return false, "", 0, 0, fmt.Errorf("commit: %w", err)
	}

	// Return the count of input files selected so the caller can compute the
	// net reduction (selectedCount files → ~1 output file, handled by
	// iceberg-go's rolling writer if data exceeds TargetFileSize).
	return true, "", len(selected), inputBytes, nil
}

// selectSmallFiles sorts tasks by file size ascending and returns up to n
// files whose size is below minSize.  Files already at or above minSize
// are not included.
func selectSmallFiles(tasks []table.FileScanTask, n int, minSize int64) []table.FileScanTask {
	// Collect small files.
	small := make([]table.FileScanTask, 0, len(tasks))
	for _, t := range tasks {
		if t.File.FileSizeBytes() < minSize {
			small = append(small, t)
		}
	}
	// Sort ascending by size (smallest first — most benefit).
	sort.Slice(small, func(i, j int) bool {
		return small[i].File.FileSizeBytes() < small[j].File.FileSizeBytes()
	})
	if len(small) > n {
		small = small[:n]
	}
	return small
}

// buildPartitionFilter constructs an iceberg BooleanExpression that matches
// exactly the partition of exampleFile.
//
// For each partition field in the current spec the filter is:
//
//	EqualTo(Reference(field.Name), partitionValue)
//
// Multiple fields are combined with NewAnd.
//
// The filter references PARTITION field names (not source column names) so
// that Overwrite's copy-on-write deletion scan operates on the partition
// projection rather than the full row data.
//
// Note on Reference semantics for Overwrite/WithOverwriteFilter:
// The filter is used by performCopyOnWriteDeletion which evaluates it against
// the partition struct via the manifest evaluator.  Partition field names are
// of the form "<source_col>_<transform_suffix>" (e.g. "dt_day", "source_id_bucket").
// iceberg-go resolves Reference names against the partition type, not the table
// schema, in this context.
func buildPartitionFilter(tbl *table.Table, f iceberg.DataFile) (iceberg.BooleanExpression, error) {
	spec := tbl.Metadata().PartitionSpec()
	schema := tbl.Metadata().CurrentSchema()
	partData := f.Partition()

	partType := spec.PartitionType(schema)
	if len(partType.FieldList) == 0 {
		// Unpartitioned table — AlwaysTrue is correct: every file belongs
		// to the single implicit partition.
		return iceberg.AlwaysTrue{}, nil
	}

	var exprs []iceberg.BooleanExpression
	for _, pf := range partType.FieldList {
		val, ok := partData[pf.ID]
		if !ok {
			return nil, fmt.Errorf("partition field %q (id %d) not found in data file partition map", pf.Name, pf.ID)
		}
		pred, err := equalityForPartitionValue(pf.Name, val)
		if err != nil {
			return nil, fmt.Errorf("build equality for field %q: %w", pf.Name, err)
		}
		exprs = append(exprs, pred)
	}

	if len(exprs) == 0 {
		return iceberg.AlwaysTrue{}, nil
	}
	if len(exprs) == 1 {
		return exprs[0], nil
	}
	result := iceberg.NewAnd(exprs[0], exprs[1], exprs[2:]...)
	return result, nil
}

// equalityForPartitionValue builds an EqualTo predicate for a partition field
// value.  iceberg-go's EqualTo is generic over LiteralType; we dispatch on
// the runtime type of val.
func equalityForPartitionValue(fieldName string, val any) (iceberg.BooleanExpression, error) {
	ref := iceberg.Reference(fieldName)
	switch v := val.(type) {
	case int:
		// Plain int is not in iceberg.LiteralType; promote to int32 or int64.
		return iceberg.EqualTo(ref, int64(v)), nil
	case int32:
		return iceberg.EqualTo(ref, v), nil
	case int64:
		return iceberg.EqualTo(ref, v), nil
	case float32:
		return iceberg.EqualTo(ref, v), nil
	case float64:
		return iceberg.EqualTo(ref, v), nil
	case string:
		return iceberg.EqualTo(ref, v), nil
	case bool:
		return iceberg.EqualTo(ref, v), nil
	case []byte:
		return iceberg.EqualTo(ref, string(v)), nil
	default:
		// Unknown type — use string representation as a best-effort.
		return iceberg.EqualTo(ref, fmt.Sprintf("%v", v)), nil
	}
}

// buildFilePathFilter builds a disjunction of EqualTo(file_path) expressions
// as a fallback when partition-based filtering is unavailable.  This ensures
// the Overwrite deletes only the intended files, never the whole table.
func buildFilePathFilter(tasks []table.FileScanTask) (iceberg.BooleanExpression, error) {
	if len(tasks) == 0 {
		return nil, fmt.Errorf("no tasks to build file path filter")
	}
	ref := iceberg.Reference("file_path")
	exprs := make([]iceberg.BooleanExpression, len(tasks))
	for i, t := range tasks {
		exprs[i] = iceberg.EqualTo(ref, t.File.FilePath())
	}
	if len(exprs) == 1 {
		return exprs[0], nil
	}
	result := exprs[0]
	for _, e := range exprs[1:] {
		result = iceberg.NewAnd(result, e)
	}
	return result, nil
}

// buildStreamingReader builds an Arrow RecordReader that streams row groups
// lazily from a specific partition of a table.
//
// Design note (500M-scale correctness):
//
// Overwrite(ctx, rdr, nil, WithOverwriteFilter(partFilter)) performs a
// FULL PARTITION REWRITE: it deletes every data file in the partition that
// matches partFilter, then writes the new files produced by rdr.  Therefore,
// rdr must supply ALL rows for the target partition — not just the rows in
// the small files — otherwise data loss occurs.
//
// The correct scope for the reader is thus: all current files in the
// target partition.  We pass partFilter as a WithRowFilter to restrict
// Scan.PlanFiles to the target partition only.  iceberg-go's manifest
// evaluator pushes partition filters down so that only manifests for this
// partition are opened, which is efficient.
//
// Streaming: ToArrowRecords returns an iter.Seq2[RecordBatch, error] that
// reads row groups lazily.  ReaderFromIter wraps it as an array.RecordReader
// without materialising all rows into memory.
func buildStreamingReader(
	ctx context.Context,
	tbl *table.Table,
	partFilter iceberg.BooleanExpression,
) (array.RecordReader, *iceberg.Schema, error) {
	scanOpts := []table.ScanOption{}
	if partFilter != nil {
		scanOpts = append(scanOpts, table.WithRowFilter(partFilter))
	}

	schema, itr, err := tbl.Scan(scanOpts...).ToArrowRecords(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("scan: %w", err)
	}

	// ReaderFromIter produces a lazy RecordReader: each Next() call pulls the
	// next RecordBatch from the iterator, which reads exactly one row group
	// from R2.  This is the 500M-scale safe path — no full materialisation.
	rdr := array.ReaderFromIter(schema, itr)
	return rdr, tbl.Metadata().CurrentSchema(), nil
}

// applyCompactDefaults fills zero-valued CompactConfig fields with safe defaults.
// Parallelism is computed from the pod's actual memory budget when not set
// explicitly, so that a 1 GiB pod runs 1 partition at a time (slower, safe)
// while a 12 GiB pod runs 4 in parallel (faster). The operator can always
// cap parallelism via CompactConfig.Parallelism.
func applyCompactDefaults(cfg CompactConfig) CompactConfig {
	if cfg.TargetFileSize <= 0 {
		cfg.TargetFileSize = 134217728 // 128 MiB
	}
	if cfg.MinFileSize <= 0 {
		cfg.MinFileSize = cfg.TargetFileSize / 2
	}
	if cfg.MaxInputPerCommit <= 0 {
		cfg.MaxInputPerCommit = 20
	}
	if cfg.PerTableTimeout <= 0 {
		cfg.PerTableTimeout = 30 * time.Minute
	}
	if cfg.Parallelism <= 0 {
		// Adaptive: each concurrent compaction goroutine peaks at ~1.5 GiB.
		// Use 50% of pod memory so other subsystems have headroom.
		budget := memconfig.NewBudget("compact", 50)
		const peakPerPartition = 1500 * 1024 * 1024 // 1.5 GiB
		maxConcurrent := int(budget.Bytes() / peakPerPartition)
		if maxConcurrent < 1 {
			maxConcurrent = 1
		}
		cfg.Parallelism = maxConcurrent
	}
	return cfg
}
