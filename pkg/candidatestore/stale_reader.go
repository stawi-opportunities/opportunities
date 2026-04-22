package candidatestore

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	iceberg "github.com/apache/iceberg-go"
	"github.com/apache/iceberg-go/catalog"
	"github.com/apache/iceberg-go/table"
)

// StaleCandidate is the output shape for stale-nudge consumers.
type StaleCandidate struct {
	CandidateID  string
	LastUploadAt time.Time
}

// StaleReader scans the candidates.cv_extracted_current Iceberg table
// for candidates whose latest CV upload occurred_at is older than the
// requested cutoff.
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

	tbl, err := r.cat.LoadTable(ctx, []string{"candidates", "cv_extracted_current"})
	if err != nil {
		return nil, fmt.Errorf("candidatestore: load cv_extracted_current: %w", err)
	}

	scan := tbl.Scan(
		table.WithRowFilter(
			iceberg.LessThan(iceberg.Reference("occurred_at"), cutoff.UnixMicro()),
		),
		table.WithSelectedFields("candidate_id", "occurred_at"),
	)

	arrowTbl, err := scan.ToArrowTable(ctx)
	if err != nil {
		return nil, fmt.Errorf("candidatestore: scan cv_extracted_current: %w", err)
	}
	defer arrowTbl.Retain()
	defer arrowTbl.Release()

	rows, err := staleRowsFromTable(arrowTbl)
	if err != nil {
		return nil, fmt.Errorf("candidatestore: decode cv_extracted_current: %w", err)
	}

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].LastUploadAt.Before(rows[j].LastUploadAt)
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

// staleRowsFromTable decodes an Arrow table with schema
// (candidate_id string, occurred_at timestamp) into []StaleCandidate.
func staleRowsFromTable(tbl arrow.Table) ([]StaleCandidate, error) {
	sc := tbl.Schema()
	cidxCandidateID := sc.FieldIndices("candidate_id")
	cidxOccurredAt := sc.FieldIndices("occurred_at")

	if len(cidxCandidateID) == 0 || len(cidxOccurredAt) == 0 {
		return nil, fmt.Errorf("cv_extracted_current: missing required columns; schema: %s", sc)
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
