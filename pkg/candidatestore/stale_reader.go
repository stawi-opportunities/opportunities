package candidatestore

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/iceberg-go/catalog"
	"github.com/apache/iceberg-go/table"
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
// Because cv_extracted is append-only (each upload appends a new row), the
// reader folds all rows per candidate_id in Go — keeping only the row with
// the latest occurred_at — then filters by cutoff. This correctly identifies
// candidates whose most recent upload is stale, even though older uploads
// might also be < cutoff.
//
// Scanning the full table is acceptable because ListStale runs weekly and
// the candidate set grows slowly relative to a weekly cadence.
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

	tbl, err := r.cat.LoadTable(ctx, []string{"candidates", "cv_extracted"})
	if err != nil {
		return nil, fmt.Errorf("candidatestore: load cv_extracted: %w", err)
	}

	// Scan all rows: we need the latest occurred_at per candidate to correctly
	// determine staleness. A pushdown filter on occurred_at < cutoff would
	// miss candidates whose most-recent upload is recent but who also have
	// older rows — those older rows would pass the pushdown and make the
	// candidate appear stale even when it isn't. Scanning all rows and folding
	// in Go avoids this false-positive.
	scan := tbl.Scan(
		table.WithSelectedFields("candidate_id", "occurred_at"),
	)

	arrowTbl, err := scan.ToArrowTable(ctx)
	if err != nil {
		return nil, fmt.Errorf("candidatestore: scan cv_extracted: %w", err)
	}
	defer arrowTbl.Release()

	rows, err := staleRowsFromTable(arrowTbl)
	if err != nil {
		return nil, fmt.Errorf("candidatestore: decode cv_extracted: %w", err)
	}

	// Fold: keep latest occurred_at per candidate_id.
	latest := make(map[string]time.Time, len(rows))
	for _, row := range rows {
		if t, ok := latest[row.CandidateID]; !ok || row.LastUploadAt.After(t) {
			latest[row.CandidateID] = row.LastUploadAt
		}
	}

	// Filter by cutoff, collect, sort oldest-first, apply limit.
	out := make([]StaleCandidate, 0, len(latest))
	for id, ts := range latest {
		if ts.Before(cutoff) {
			out = append(out, StaleCandidate{CandidateID: id, LastUploadAt: ts})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].LastUploadAt.Before(out[j].LastUploadAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// staleRowsFromTable decodes an Arrow table with schema
// (candidate_id string, occurred_at timestamp) into []StaleCandidate.
func staleRowsFromTable(tbl arrow.Table) ([]StaleCandidate, error) {
	sc := tbl.Schema()
	cidxCandidateID := sc.FieldIndices("candidate_id")
	cidxOccurredAt := sc.FieldIndices("occurred_at")

	if len(cidxCandidateID) == 0 || len(cidxOccurredAt) == 0 {
		return nil, fmt.Errorf("cv_extracted: missing required columns; schema: %s", sc)
	}

	tr := array.NewTableReader(tbl, -1)
	defer tr.Release()

	var out []StaleCandidate
	for tr.Next() {
		rec := tr.Record()
		nRows := int(rec.NumRows())

		colCandidateID := stringColOrNil(rec, cidxCandidateID[0])
		colOccurredAt := timestampColOrNil(rec, cidxOccurredAt[0])

		for i := 0; i < nRows; i++ {
			if colCandidateID == nil || colCandidateID.IsNull(i) {
				continue
			}
			candidateID := colCandidateID.Value(i)
			if candidateID == "" {
				continue
			}
			var occurredAt time.Time
			if colOccurredAt != nil && !colOccurredAt.IsNull(i) {
				occurredAt = timestampToTime(rec, cidxOccurredAt[0], colOccurredAt, i)
			}
			out = append(out, StaleCandidate{
				CandidateID:  candidateID,
				LastUploadAt: occurredAt,
			})
		}
	}
	return out, nil
}

// timestampColOrNil returns the Timestamp column at colIdx, or nil if
// the column is absent or has an unexpected type.
func timestampColOrNil(rec arrow.Record, colIdx int) *array.Timestamp {
	if colIdx < 0 || colIdx >= int(rec.NumCols()) {
		return nil
	}
	c, _ := rec.Column(colIdx).(*array.Timestamp)
	return c
}

// timestampToTime converts row i of a Timestamp column to time.Time,
// respecting the column's TimeUnit from the schema.
func timestampToTime(rec arrow.Record, colIdx int, col *array.Timestamp, i int) time.Time {
	ts := col.Value(i)
	field := rec.Schema().Field(colIdx)
	if tsType, ok := field.Type.(*arrow.TimestampType); ok {
		toTime, err := tsType.GetToTimeFunc()
		if err == nil {
			return toTime(ts)
		}
		return ts.ToTime(tsType.Unit)
	}
	// Fallback: assume microseconds (Iceberg default).
	return ts.ToTime(arrow.Microsecond)
}
