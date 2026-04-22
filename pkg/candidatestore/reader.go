// Package candidatestore provides read-only access to the latest
// per-candidate derived state (embedding, preferences) from Iceberg
// tables. The match endpoint uses it to avoid reading Postgres on the
// hot path.
//
// The *_current tables are rebuilt nightly by the iceberg-ops maintenance
// job (Wave 7). Acceptable staleness for the match endpoint and stale-
// nudge admin is the nightly compaction window — see design spec §4.
package candidatestore

import (
	"context"
	"errors"
	"fmt"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	iceberg "github.com/apache/iceberg-go"
	"github.com/apache/iceberg-go/catalog"
	"github.com/apache/iceberg-go/table"

	eventsv1 "stawi.jobs/pkg/events/v1"
)

// ErrNotFound is returned when no embedding/preferences row exists
// for the requested candidate in the Iceberg table.
var ErrNotFound = errors.New("candidatestore: candidate state not found")

// Reader reads candidate embedding + preference state from Iceberg
// *_current tables.
type Reader struct {
	cat catalog.Catalog
}

// NewReader builds a Reader backed by an Iceberg catalog.
func NewReader(cat catalog.Catalog) *Reader {
	return &Reader{cat: cat}
}

// LatestEmbedding returns the embedding row for candidateID from the
// candidates.embeddings_current Iceberg table. Because *_current holds
// exactly one row per candidate after the nightly MERGE, the scan
// returns at most one row.
//
// Returns (zero, ErrNotFound) when no row exists for the candidate.
// Returns (zero, nil) only when the table scan returns zero rows but no
// error — callers treat that identically to ErrNotFound.
func (r *Reader) LatestEmbedding(ctx context.Context, candidateID string) (eventsv1.CandidateEmbeddingV1, error) {
	tbl, err := r.cat.LoadTable(ctx, []string{"candidates", "embeddings_current"})
	if err != nil {
		return eventsv1.CandidateEmbeddingV1{}, fmt.Errorf("candidatestore: load embeddings_current: %w", err)
	}

	scan := tbl.Scan(
		table.WithRowFilter(iceberg.EqualTo(iceberg.Reference("candidate_id"), candidateID)),
		table.WithSelectedFields("candidate_id", "cv_version", "vector", "model_version"),
	)

	arrowTbl, err := scan.ToArrowTable(ctx)
	if err != nil {
		return eventsv1.CandidateEmbeddingV1{}, fmt.Errorf("candidatestore: scan embeddings_current: %w", err)
	}
	defer arrowTbl.Release()

	if arrowTbl.NumRows() == 0 {
		return eventsv1.CandidateEmbeddingV1{}, ErrNotFound
	}

	rows, err := embeddingRowsFromTable(arrowTbl)
	if err != nil {
		return eventsv1.CandidateEmbeddingV1{}, fmt.Errorf("candidatestore: decode embeddings: %w", err)
	}
	if len(rows) == 0 {
		return eventsv1.CandidateEmbeddingV1{}, ErrNotFound
	}

	// _current has at most one row per candidate. If somehow there are
	// multiple (compaction lag), pick the one with the highest CVVersion.
	best := rows[0]
	for _, row := range rows[1:] {
		if row.CVVersion > best.CVVersion {
			best = row
		}
	}
	return best, nil
}

// LatestPreferences returns the preferences row for candidateID from
// the candidates.preferences_current Iceberg table.
//
// Returns (zero, ErrNotFound) when no row exists.
func (r *Reader) LatestPreferences(ctx context.Context, candidateID string) (eventsv1.PreferencesUpdatedV1, error) {
	tbl, err := r.cat.LoadTable(ctx, []string{"candidates", "preferences_current"})
	if err != nil {
		return eventsv1.PreferencesUpdatedV1{}, fmt.Errorf("candidatestore: load preferences_current: %w", err)
	}

	scan := tbl.Scan(
		table.WithRowFilter(iceberg.EqualTo(iceberg.Reference("candidate_id"), candidateID)),
		table.WithSelectedFields(
			"candidate_id", "remote_preference",
			"salary_min", "salary_max", "currency",
			"preferred_locations", "excluded_companies",
			"target_roles", "languages", "availability",
		),
	)

	arrowTbl, err := scan.ToArrowTable(ctx)
	if err != nil {
		return eventsv1.PreferencesUpdatedV1{}, fmt.Errorf("candidatestore: scan preferences_current: %w", err)
	}
	defer arrowTbl.Release()

	if arrowTbl.NumRows() == 0 {
		return eventsv1.PreferencesUpdatedV1{}, ErrNotFound
	}

	rows, err := preferencesRowsFromTable(arrowTbl)
	if err != nil {
		return eventsv1.PreferencesUpdatedV1{}, fmt.Errorf("candidatestore: decode preferences: %w", err)
	}
	if len(rows) == 0 {
		return eventsv1.PreferencesUpdatedV1{}, ErrNotFound
	}
	return rows[0], nil
}

// --- Arrow table decoders ---

// embeddingRowsFromTable reads all rows from an Arrow table that has
// the embeddings_current schema (candidate_id, cv_version, vector,
// model_version).
func embeddingRowsFromTable(tbl arrow.Table) ([]eventsv1.CandidateEmbeddingV1, error) {
	sc := tbl.Schema()
	cidxCandidateID := sc.FieldIndices("candidate_id")
	cidxCVVersion := sc.FieldIndices("cv_version")
	cidxVector := sc.FieldIndices("vector")
	cidxModelVersion := sc.FieldIndices("model_version")

	if len(cidxCandidateID) == 0 || len(cidxVector) == 0 {
		return nil, fmt.Errorf("embeddings_current: missing required columns (candidate_id, vector); schema: %s", sc)
	}

	tr := array.NewTableReader(tbl, -1)
	defer tr.Release()

	var out []eventsv1.CandidateEmbeddingV1
	for tr.Next() {
		rec := tr.Record()
		nRows := int(rec.NumRows())

		colCandidateID := stringColOrNil(rec, cidxCandidateID[0])
		colVector := listColOrNil(rec, cidxVector[0])

		var colCVVersion *array.Int32
		if len(cidxCVVersion) > 0 {
			if c, ok := rec.Column(cidxCVVersion[0]).(*array.Int32); ok {
				colCVVersion = c
			}
		}
		var colModelVersion *array.String
		if len(cidxModelVersion) > 0 {
			if c, ok := rec.Column(cidxModelVersion[0]).(*array.String); ok {
				colModelVersion = c
			}
		}

		for i := 0; i < nRows; i++ {
			var row eventsv1.CandidateEmbeddingV1
			if colCandidateID != nil && !colCandidateID.IsNull(i) {
				row.CandidateID = colCandidateID.Value(i)
			}
			if colCVVersion != nil && !colCVVersion.IsNull(i) {
				row.CVVersion = int(colCVVersion.Value(i))
			}
			if colVector != nil && !colVector.IsNull(i) {
				row.Vector = listFloat32Values(colVector, i)
			}
			if colModelVersion != nil && !colModelVersion.IsNull(i) {
				row.ModelVersion = colModelVersion.Value(i)
			}
			out = append(out, row)
		}
	}
	return out, nil
}

// preferencesRowsFromTable reads all rows from an Arrow table that has
// the preferences_current schema.
func preferencesRowsFromTable(tbl arrow.Table) ([]eventsv1.PreferencesUpdatedV1, error) {
	sc := tbl.Schema()
	cidxCandidateID := sc.FieldIndices("candidate_id")
	if len(cidxCandidateID) == 0 {
		return nil, fmt.Errorf("preferences_current: missing required column candidate_id; schema: %s", sc)
	}

	tr := array.NewTableReader(tbl, -1)
	defer tr.Release()

	var out []eventsv1.PreferencesUpdatedV1
	for tr.Next() {
		rec := tr.Record()
		nRows := int(rec.NumRows())

		colCandidateID := stringColOrNil(rec, cidxCandidateID[0])

		// Optional columns: extract by name so missing columns are gracefully skipped.
		colRemote := stringColByName(rec, sc, "remote_preference")
		colCurrency := stringColByName(rec, sc, "currency")
		colAvailability := stringColByName(rec, sc, "availability")
		colSalaryMin := int32ColByName(rec, sc, "salary_min")
		colSalaryMax := int32ColByName(rec, sc, "salary_max")
		colPrefLoc := listColByName(rec, sc, "preferred_locations")
		colExcluded := listColByName(rec, sc, "excluded_companies")
		colTargetRoles := listColByName(rec, sc, "target_roles")
		colLanguages := listColByName(rec, sc, "languages")

		for i := 0; i < nRows; i++ {
			var row eventsv1.PreferencesUpdatedV1
			if colCandidateID != nil && !colCandidateID.IsNull(i) {
				row.CandidateID = colCandidateID.Value(i)
			}
			if colRemote != nil && !colRemote.IsNull(i) {
				row.RemotePreference = colRemote.Value(i)
			}
			if colCurrency != nil && !colCurrency.IsNull(i) {
				row.Currency = colCurrency.Value(i)
			}
			if colAvailability != nil && !colAvailability.IsNull(i) {
				row.Availability = colAvailability.Value(i)
			}
			if colSalaryMin != nil && !colSalaryMin.IsNull(i) {
				row.SalaryMin = int(colSalaryMin.Value(i))
			}
			if colSalaryMax != nil && !colSalaryMax.IsNull(i) {
				row.SalaryMax = int(colSalaryMax.Value(i))
			}
			if colPrefLoc != nil && !colPrefLoc.IsNull(i) {
				row.PreferredLocations = listStringValues(colPrefLoc, i)
			}
			if colExcluded != nil && !colExcluded.IsNull(i) {
				row.ExcludedCompanies = listStringValues(colExcluded, i)
			}
			if colTargetRoles != nil && !colTargetRoles.IsNull(i) {
				row.TargetRoles = listStringValues(colTargetRoles, i)
			}
			if colLanguages != nil && !colLanguages.IsNull(i) {
				row.Languages = listStringValues(colLanguages, i)
			}
			out = append(out, row)
		}
	}
	return out, nil
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

func stringColByName(rec arrow.Record, sc *arrow.Schema, name string) *array.String {
	idxs := sc.FieldIndices(name)
	if len(idxs) == 0 {
		return nil
	}
	return stringColOrNil(rec, idxs[0])
}

func int32ColByName(rec arrow.Record, sc *arrow.Schema, name string) *array.Int32 {
	idxs := sc.FieldIndices(name)
	if len(idxs) == 0 {
		return nil
	}
	if idxs[0] < 0 || idxs[0] >= int(rec.NumCols()) {
		return nil
	}
	c, _ := rec.Column(idxs[0]).(*array.Int32)
	return c
}

func listColByName(rec arrow.Record, sc *arrow.Schema, name string) *array.List {
	idxs := sc.FieldIndices(name)
	if len(idxs) == 0 {
		return nil
	}
	return listColOrNil(rec, idxs[0])
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
