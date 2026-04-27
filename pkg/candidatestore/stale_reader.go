package candidatestore

import (
	"container/heap"
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	iceberg "github.com/apache/iceberg-go"
	"github.com/apache/iceberg-go/catalog"
	"github.com/apache/iceberg-go/table"

	"github.com/stawi-opportunities/opportunities/pkg/memconfig"
)

// StaleCandidate is the output shape for stale-nudge consumers.
type StaleCandidate struct {
	CandidateID  string
	LastUploadAt time.Time
}

// StaleReader scans the append-only candidates.cv_extracted Iceberg table
// for candidates whose latest CV upload occurred_at is older than the
// requested cutoff.
//
// Memory model (O(batch), not O(partition)):
//
//   - A memconfig.Budget("stale-reader", 20) governs the maximum number of
//     candidate entries held in memory at once.
//   - When the bounded map reaches maxKeysInMemory, it is flushed: entries
//     whose ts < cutoff are offered to the bounded top-N heap; then the map
//     is reset.
//   - The heap stays bounded to the caller's limit parameter.
//
// Trade-off: a candidate whose older upload is flushed as "stale" before
// their newer upload is processed will appear in the top-N heap. The flush
// logic removes them from the heap if a newer, non-stale upload is seen
// later (retract). For candidates near the boundary this may produce a small
// rate of over-report: they receive a nudge email even though they just
// uploaded. This is acceptable — the nudge is graceful and they will simply
// ignore it.
type StaleReader struct {
	cat catalog.Catalog
}

// NewStaleReader constructs a StaleReader backed by an Iceberg catalog.
func NewStaleReader(cat catalog.Catalog) *StaleReader {
	return &StaleReader{cat: cat}
}

// ListStale returns candidates whose most-recent CV upload is older
// than cutoff. Returns up to limit rows sorted ascending by
// LastUploadAt so the oldest get nudged first.
func (r *StaleReader) ListStale(ctx context.Context, cutoff time.Time, limit int) ([]StaleCandidate, error) {
	if limit <= 0 {
		limit = 1000
	}

	budget := memconfig.NewBudget("stale-reader", 20)
	maxKeysInMemory := budget.BatchSizeFor(32)

	tbl, err := r.cat.LoadTable(ctx, []string{"candidates", "cv_extracted"})
	if err != nil {
		return nil, fmt.Errorf("candidatestore: load cv_extracted: %w", err)
	}

	numBuckets, bucketFieldName, err := staleBucketPartitionInfo(tbl, "candidate_id")
	if err != nil {
		return nil, fmt.Errorf("candidatestore: partition info: %w", err)
	}

	// top-N max-heap on LastUploadAt: keeps the oldest `limit` candidates.
	// (max-heap so the newest entry is cheaply evictable when limit is exceeded)
	topN := newStaleHeap(limit)

	for bucketIdx := 0; bucketIdx < numBuckets; bucketIdx++ {
		var scanOpts []table.ScanOption
		if bucketFieldName != "" {
			partFilter := iceberg.EqualTo(iceberg.Reference(bucketFieldName), int32(bucketIdx))
			scanOpts = append(scanOpts, table.WithRowFilter(partFilter))
		}
		scanOpts = append(scanOpts, table.WithSelectedFields("candidate_id", "occurred_at"))

		scan := tbl.Scan(scanOpts...)

		_, itr, err := scan.ToArrowRecords(ctx)
		if err != nil {
			return nil, fmt.Errorf("candidatestore: to arrow records (bucket %d): %w", bucketIdx, err)
		}

		// bounded map: never exceeds maxKeysInMemory entries at once.
		bounded := make(map[string]time.Time, maxKeysInMemory)

		for batch, batchErr := range itr {
			if batchErr != nil {
				return nil, fmt.Errorf("candidatestore: iterate batch (bucket %d): %w", bucketIdx, batchErr)
			}
			foldStaleBatch(batch, bounded)
			batch.Release()

			if len(bounded) >= maxKeysInMemory {
				flushStaleMap(bounded, cutoff, topN)
				bounded = make(map[string]time.Time, maxKeysInMemory)
			}
		}

		// Flush remaining entries for this partition.
		flushStaleMap(bounded, cutoff, topN)
	}

	out := topN.sorted()
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastUploadAt.Before(out[j].LastUploadAt)
	})
	return out, nil
}

// flushStaleMap offers each entry in m to topN if its ts < cutoff,
// and retracts any heap entry that has been superseded by a non-stale ts.
func flushStaleMap(m map[string]time.Time, cutoff time.Time, topN *staleHeap) {
	for id, ts := range m {
		if ts.Before(cutoff) {
			topN.offerOrRetract(StaleCandidate{CandidateID: id, LastUploadAt: ts}, cutoff)
		} else {
			// This candidate has a non-stale upload — retract from heap if present.
			topN.retract(id)
		}
	}
}

// foldStaleBatch updates latest with the max occurred_at per candidate_id from
// one Arrow RecordBatch.
func foldStaleBatch(rec arrow.RecordBatch, latest map[string]time.Time) {
	sc := rec.Schema()
	cidxCandidateID := sc.FieldIndices("candidate_id")
	cidxOccurredAt := sc.FieldIndices("occurred_at")
	if len(cidxCandidateID) == 0 || len(cidxOccurredAt) == 0 {
		return
	}

	colCandidateID := staleStringCol(rec, cidxCandidateID[0])
	colOccurredAt := staleTimestampCol(rec, cidxOccurredAt[0])

	for i := 0; i < int(rec.NumRows()); i++ {
		if colCandidateID == nil || colCandidateID.IsNull(i) {
			continue
		}
		id := colCandidateID.Value(i)
		if id == "" {
			continue
		}
		var ts time.Time
		if colOccurredAt != nil && !colOccurredAt.IsNull(i) {
			ts = staleTimestampToTime(rec, cidxOccurredAt[0], colOccurredAt, i)
		}
		if prev, ok := latest[id]; !ok || ts.After(prev) {
			latest[id] = ts
		}
	}
}

// ---------------------------------------------------------------------------
// Partition helper
// ---------------------------------------------------------------------------

// staleBucketPartitionInfo inspects tbl's partition spec for a bucket transform
// on sourceField. Returns (N, fieldName, nil) if found, or (1, "", nil) if the
// table is unpartitioned on that field (safe fallback: single full-table pass).
func staleBucketPartitionInfo(tbl *table.Table, sourceField string) (int, string, error) {
	spec := tbl.Metadata().PartitionSpec()
	schema := tbl.Metadata().CurrentSchema()
	partType := spec.PartitionType(schema)

	for _, pf := range partType.FieldList {
		name := pf.Name
		suffix := "_bucket"
		if len(name) >= len(sourceField)+len(suffix) &&
			name[:len(sourceField)] == sourceField &&
			staleContainsStr(name[len(sourceField):], "bucket") {
			for sf := range spec.Fields() {
				if sf.Name == name {
					if bt, ok := sf.Transform.(iceberg.BucketTransform); ok {
						return bt.NumBuckets, name, nil
					}
				}
			}
			// Bucket field found but transform type assertion failed; default N=32.
			return 32, name, nil
		}
	}
	// Not bucket-partitioned on sourceField; scan as a single pass.
	return 1, "", nil
}

func staleContainsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Top-N max-heap (retains the `limit` most-stale / oldest candidates)
//
// We use a max-heap keyed on LastUploadAt so the *newest* (least-stale) entry
// sits at the top and is cheaply evicted when len > limit. After draining all
// partitions the remaining entries are the oldest ones.
//
// index maps candidateID → position in data so that offerOrRetract and retract
// can update or remove entries when a newer (or non-stale) upload is seen after
// an earlier bounded-map flush.
// ---------------------------------------------------------------------------

type staleHeap struct {
	data  []StaleCandidate
	index map[string]int // candidateID → current position in data
	limit int
}

func newStaleHeap(limit int) *staleHeap {
	h := &staleHeap{
		limit: limit,
		data:  make([]StaleCandidate, 0, limit+1),
		index: make(map[string]int, limit+1),
	}
	heap.Init(h)
	return h
}

func (h *staleHeap) Len() int { return len(h.data) }

// Less: max-heap on LastUploadAt — newest entry at root so it can be evicted.
func (h *staleHeap) Less(i, j int) bool {
	return h.data[i].LastUploadAt.After(h.data[j].LastUploadAt)
}
func (h *staleHeap) Swap(i, j int) {
	h.data[i], h.data[j] = h.data[j], h.data[i]
	h.index[h.data[i].CandidateID] = i
	h.index[h.data[j].CandidateID] = j
}
func (h *staleHeap) Push(x any) {
	c := x.(StaleCandidate)
	h.index[c.CandidateID] = len(h.data)
	h.data = append(h.data, c)
}
func (h *staleHeap) Pop() any {
	n := len(h.data)
	x := h.data[n-1]
	h.data = h.data[:n-1]
	delete(h.index, x.CandidateID)
	return x
}

// offer adds c to the heap; if limit is exceeded, evicts the newest entry.
func (h *staleHeap) offer(c StaleCandidate) {
	heap.Push(h, c)
	if h.Len() > h.limit {
		heap.Pop(h) // removes the root = newest (least-stale) entry
	}
}

// offerOrRetract inserts or updates c in the heap.
//
// If the candidate already exists and the new entry has a newer LastUploadAt,
// the heap entry is updated via heap.Fix. If after the update the new
// timestamp is >= cutoff (the candidate is no longer stale), the entry is
// removed from the heap entirely — it was invalidated by a later non-stale
// re-upload that arrived in a subsequent bounded-map flush.
//
// If the candidate doesn't yet exist in the heap, it is inserted as a fresh
// offer (only if ts < cutoff; callers guarantee this).
func (h *staleHeap) offerOrRetract(c StaleCandidate, cutoff time.Time) {
	if pos, exists := h.index[c.CandidateID]; exists {
		if c.LastUploadAt.After(h.data[pos].LastUploadAt) {
			h.data[pos] = c
			heap.Fix(h, pos)
			// If the updated timestamp is now >= cutoff the candidate is no
			// longer stale — remove it from the heap.
			if !c.LastUploadAt.Before(cutoff) {
				h.removeByID(c.CandidateID)
			}
		}
		return
	}
	h.offer(c)
}

// removeByID removes the entry for candidateID from the heap (if present).
// Identical to retract but exported for internal reuse without repeating
// the index lookup.
func (h *staleHeap) removeByID(candidateID string) {
	pos, exists := h.index[candidateID]
	if !exists {
		return
	}
	heap.Remove(h, pos)
}

// retract removes the entry for candidateID from the heap if present.
// Used when a non-stale upload is seen for a candidate previously flushed
// as stale.
func (h *staleHeap) retract(candidateID string) {
	pos, exists := h.index[candidateID]
	if !exists {
		return
	}
	heap.Remove(h, pos)
}

// sorted returns a copy of the heap contents as a plain slice.
func (h *staleHeap) sorted() []StaleCandidate {
	out := make([]StaleCandidate, len(h.data))
	copy(out, h.data)
	return out
}

// ---------------------------------------------------------------------------
// Arrow column helpers (local; the package-level helpers in reader.go cover
// different column types. These are minimal versions for (candidate_id,
// occurred_at) only.)
// ---------------------------------------------------------------------------

func staleStringCol(rec arrow.RecordBatch, colIdx int) *array.String {
	if colIdx < 0 || colIdx >= int(rec.NumCols()) {
		return nil
	}
	c, _ := rec.Column(colIdx).(*array.String)
	return c
}

func staleTimestampCol(rec arrow.RecordBatch, colIdx int) *array.Timestamp {
	if colIdx < 0 || colIdx >= int(rec.NumCols()) {
		return nil
	}
	c, _ := rec.Column(colIdx).(*array.Timestamp)
	return c
}

// staleTimestampToTime converts one Timestamp cell to time.Time,
// respecting the column's TimeUnit from the schema.
func staleTimestampToTime(rec arrow.RecordBatch, colIdx int, col *array.Timestamp, i int) time.Time {
	ts := col.Value(i)
	field := rec.Schema().Field(colIdx)
	if tsType, ok := field.Type.(*arrow.TimestampType); ok {
		if fn, err := tsType.GetToTimeFunc(); err == nil {
			return fn(ts)
		}
		return ts.ToTime(tsType.Unit)
	}
	// Fallback: Iceberg default is microseconds.
	return ts.ToTime(arrow.Microsecond)
}

// timestampToTime converts row i of a Timestamp column to time.Time,
// respecting the column's TimeUnit from the schema. Used by reader.go.
func timestampToTime(rec arrow.RecordBatch, colIdx int, col *array.Timestamp, i int) time.Time {
	return staleTimestampToTime(rec, colIdx, col, i)
}
