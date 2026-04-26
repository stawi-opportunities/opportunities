// Package candidatestore provides read-only access to the latest
// per-candidate derived state (embedding, preferences) from Iceberg
// append-only tables. The match endpoint uses it to avoid reading
// Postgres on the hot path.
//
// Staleness: each call scans the append-only table filtered by
// candidate_id. Iceberg's bucket(32, candidate_id) partition plus a
// bloom filter on candidate_id limits the scan to ~1/32 of files.
// Target latency <100ms p95 for single-candidate lookups.
package candidatestore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	iceberg "github.com/apache/iceberg-go"
	"github.com/apache/iceberg-go/catalog"
	"github.com/apache/iceberg-go/table"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

// ErrNotFound is returned when no embedding/preferences row exists
// for the requested candidate in the Iceberg table.
var ErrNotFound = errors.New("candidatestore: candidate state not found")

// ErrEmbeddingNotFound is returned specifically when no embedding row exists.
var ErrEmbeddingNotFound = fmt.Errorf("%w: no embedding row", ErrNotFound)

// Reader reads candidate embedding + preference state from Iceberg
// append-only tables, folding in Go to find the latest row per candidate.
type Reader struct {
	cat catalog.Catalog
}

// NewReader builds a Reader backed by an Iceberg catalog.
func NewReader(cat catalog.Catalog) *Reader {
	return &Reader{cat: cat}
}

// LatestEmbedding returns the embedding row for candidateID from the
// append-only candidates.embeddings Iceberg table. Rows are streamed
// batch-by-batch (ToArrowRecords); only the latest row by occurred_at is
// retained in memory at any time, so memory is O(1) regardless of how many
// CV versions the candidate has accumulated.
//
// Returns (zero, ErrEmbeddingNotFound) when no row exists for the candidate.
func (r *Reader) LatestEmbedding(ctx context.Context, candidateID string) (eventsv1.CandidateEmbeddingV1, error) {
	tbl, err := r.cat.LoadTable(ctx, []string{"candidates", "embeddings"})
	if err != nil {
		return eventsv1.CandidateEmbeddingV1{}, fmt.Errorf("candidatestore: load embeddings: %w", err)
	}

	scan := tbl.Scan(
		table.WithRowFilter(iceberg.EqualTo(iceberg.Reference("candidate_id"), candidateID)),
		table.WithSelectedFields("candidate_id", "cv_version", "vector", "model_version", "occurred_at"),
	)

	_, itr, err := scan.ToArrowRecords(ctx)
	if err != nil {
		return eventsv1.CandidateEmbeddingV1{}, fmt.Errorf("candidatestore: scan embeddings: %w", err)
	}

	var latest eventsv1.CandidateEmbeddingV1
	var latestOccurred time.Time
	found := false

	for batch, batchErr := range itr {
		if batchErr != nil {
			return eventsv1.CandidateEmbeddingV1{}, fmt.Errorf("candidatestore: iterate embeddings: %w", batchErr)
		}
		sc := batch.Schema()
		cidxCandidateID := sc.FieldIndices("candidate_id")
		cidxVector := sc.FieldIndices("vector")
		if len(cidxCandidateID) == 0 || len(cidxVector) == 0 {
			batch.Release()
			continue
		}
		nRows := int(batch.NumRows())
		for i := 0; i < nRows; i++ {
			row := decodeEmbeddingRow(batch, sc, i)
			if !found || row.OccurredAt.After(latestOccurred) {
				latest = row
				latestOccurred = row.OccurredAt
				found = true
			}
		}
		batch.Release()
	}

	if !found {
		return eventsv1.CandidateEmbeddingV1{}, ErrEmbeddingNotFound
	}
	return latest, nil
}

// LatestPreferences returns the preferences row for candidateID from the
// append-only candidates.preferences Iceberg table. Rows are streamed
// batch-by-batch (ToArrowRecords); only the latest row by occurred_at is
// retained in memory at any time.
//
// Returns (zero, ErrNotFound) when no row exists.
func (r *Reader) LatestPreferences(ctx context.Context, candidateID string) (eventsv1.PreferencesUpdatedV1, error) {
	tbl, err := r.cat.LoadTable(ctx, []string{"candidates", "preferences"})
	if err != nil {
		return eventsv1.PreferencesUpdatedV1{}, fmt.Errorf("candidatestore: load preferences: %w", err)
	}

	scan := tbl.Scan(
		table.WithRowFilter(iceberg.EqualTo(iceberg.Reference("candidate_id"), candidateID)),
		table.WithSelectedFields(
			"candidate_id", "remote_preference",
			"salary_min", "salary_max", "currency",
			"preferred_locations", "excluded_companies",
			"target_roles", "languages", "availability", "occurred_at",
		),
	)

	_, itr, err := scan.ToArrowRecords(ctx)
	if err != nil {
		return eventsv1.PreferencesUpdatedV1{}, fmt.Errorf("candidatestore: scan preferences: %w", err)
	}

	var latest eventsv1.PreferencesUpdatedV1
	var latestOccurred time.Time
	found := false

	for batch, batchErr := range itr {
		if batchErr != nil {
			return eventsv1.PreferencesUpdatedV1{}, fmt.Errorf("candidatestore: iterate preferences: %w", batchErr)
		}
		sc := batch.Schema()
		cidxCandidateID := sc.FieldIndices("candidate_id")
		if len(cidxCandidateID) == 0 {
			batch.Release()
			continue
		}
		nRows := int(batch.NumRows())
		for i := 0; i < nRows; i++ {
			row := decodePreferencesRow(batch, sc, i)
			if !found || row.OccurredAt.After(latestOccurred) {
				latest = row
				latestOccurred = row.OccurredAt
				found = true
			}
		}
		batch.Release()
	}

	if !found {
		return eventsv1.PreferencesUpdatedV1{}, ErrNotFound
	}
	return latest, nil
}

// --- per-row decoders (operate on a single Arrow RecordBatch row) ---

// decodeEmbeddingRow extracts one row from a RecordBatch that has the
// embeddings schema (candidate_id, cv_version, vector, model_version, occurred_at).
func decodeEmbeddingRow(rec arrow.Record, sc *arrow.Schema, i int) eventsv1.CandidateEmbeddingV1 {
	var row eventsv1.CandidateEmbeddingV1

	if idxs := sc.FieldIndices("candidate_id"); len(idxs) > 0 {
		if col := stringColOrNil(rec, idxs[0]); col != nil && !col.IsNull(i) {
			row.CandidateID = col.Value(i)
		}
	}
	if idxs := sc.FieldIndices("cv_version"); len(idxs) > 0 {
		if col, ok := rec.Column(idxs[0]).(*array.Int32); ok && !col.IsNull(i) {
			row.CVVersion = int(col.Value(i))
		}
	}
	if idxs := sc.FieldIndices("vector"); len(idxs) > 0 {
		if col := listColOrNil(rec, idxs[0]); col != nil && !col.IsNull(i) {
			row.Vector = listFloat32Values(col, i)
		}
	}
	if idxs := sc.FieldIndices("model_version"); len(idxs) > 0 {
		if col := stringColOrNil(rec, idxs[0]); col != nil && !col.IsNull(i) {
			row.ModelVersion = col.Value(i)
		}
	}
	if idxs := sc.FieldIndices("occurred_at"); len(idxs) > 0 {
		if col, ok := rec.Column(idxs[0]).(*array.Timestamp); ok && !col.IsNull(i) {
			row.OccurredAt = timestampToTime(rec, idxs[0], col, i)
		}
	}
	return row
}

// decodePreferencesRow extracts one row from a RecordBatch that has the
// preferences schema.
func decodePreferencesRow(rec arrow.Record, sc *arrow.Schema, i int) eventsv1.PreferencesUpdatedV1 {
	var row eventsv1.PreferencesUpdatedV1

	if idxs := sc.FieldIndices("candidate_id"); len(idxs) > 0 {
		if col := stringColOrNil(rec, idxs[0]); col != nil && !col.IsNull(i) {
			row.CandidateID = col.Value(i)
		}
	}
	if idxs := sc.FieldIndices("remote_preference"); len(idxs) > 0 {
		if col := stringColOrNil(rec, idxs[0]); col != nil && !col.IsNull(i) {
			row.RemotePreference = col.Value(i)
		}
	}
	if idxs := sc.FieldIndices("currency"); len(idxs) > 0 {
		if col := stringColOrNil(rec, idxs[0]); col != nil && !col.IsNull(i) {
			row.Currency = col.Value(i)
		}
	}
	if idxs := sc.FieldIndices("availability"); len(idxs) > 0 {
		if col := stringColOrNil(rec, idxs[0]); col != nil && !col.IsNull(i) {
			row.Availability = col.Value(i)
		}
	}
	if idxs := sc.FieldIndices("salary_min"); len(idxs) > 0 {
		if col, ok := rec.Column(idxs[0]).(*array.Int32); ok && !col.IsNull(i) {
			row.SalaryMin = int(col.Value(i))
		}
	}
	if idxs := sc.FieldIndices("salary_max"); len(idxs) > 0 {
		if col, ok := rec.Column(idxs[0]).(*array.Int32); ok && !col.IsNull(i) {
			row.SalaryMax = int(col.Value(i))
		}
	}
	if idxs := sc.FieldIndices("preferred_locations"); len(idxs) > 0 {
		if col := listColOrNil(rec, idxs[0]); col != nil && !col.IsNull(i) {
			row.PreferredLocations = listStringValues(col, i)
		}
	}
	if idxs := sc.FieldIndices("excluded_companies"); len(idxs) > 0 {
		if col := listColOrNil(rec, idxs[0]); col != nil && !col.IsNull(i) {
			row.ExcludedCompanies = listStringValues(col, i)
		}
	}
	if idxs := sc.FieldIndices("target_roles"); len(idxs) > 0 {
		if col := listColOrNil(rec, idxs[0]); col != nil && !col.IsNull(i) {
			row.TargetRoles = listStringValues(col, i)
		}
	}
	if idxs := sc.FieldIndices("languages"); len(idxs) > 0 {
		if col := listColOrNil(rec, idxs[0]); col != nil && !col.IsNull(i) {
			row.Languages = listStringValues(col, i)
		}
	}
	if idxs := sc.FieldIndices("occurred_at"); len(idxs) > 0 {
		if col, ok := rec.Column(idxs[0]).(*array.Timestamp); ok && !col.IsNull(i) {
			row.OccurredAt = timestampToTime(rec, idxs[0], col, i)
		}
	}
	return row
}

// --- low-level Arrow column helpers ---

func stringColOrNil(rec arrow.Record, colIdx int) *array.String {
	if colIdx < 0 || colIdx >= int(rec.NumCols()) {
		return nil
	}
	c, _ := rec.Column(colIdx).(*array.String)
	return c
}

func listColOrNil(rec arrow.Record, colIdx int) *array.List {
	if colIdx < 0 || colIdx >= int(rec.NumCols()) {
		return nil
	}
	c, _ := rec.Column(colIdx).(*array.List)
	return c
}

// listFloat32Values extracts []float32 from row i of a List<float32> column.
func listFloat32Values(col *array.List, i int) []float32 {
	start, end := col.ValueOffsets(i)
	vals, ok := col.ListValues().(*array.Float32)
	if !ok || start >= end {
		return nil
	}
	raw := vals.Float32Values()
	if int(end) > len(raw) {
		return nil
	}
	out := make([]float32, end-start)
	copy(out, raw[start:end])
	return out
}

// listStringValues extracts []string from row i of a List<string> column.
func listStringValues(col *array.List, i int) []string {
	start, end := col.ValueOffsets(i)
	vals, ok := col.ListValues().(*array.String)
	if !ok {
		return nil
	}
	out := make([]string, 0, end-start)
	for j := int(start); j < int(end); j++ {
		if vals.IsNull(j) {
			out = append(out, "")
		} else {
			out = append(out, vals.Value(j))
		}
	}
	return out
}
