package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	iceberg "github.com/apache/iceberg-go"
	"github.com/apache/iceberg-go/catalog"
	"github.com/apache/iceberg-go/table"
	"github.com/pitabwire/util"
)

// r2Snapshotter is the minimal publish interface the backfill needs.
type r2Snapshotter interface {
	UploadPublicSnapshot(ctx context.Context, key string, body []byte) error
	TriggerDeploy() error
}

// backfillIcebergHandler scans jobs.canonicals_current via Iceberg and
// publishes Hugo snapshots under jobs/<slug>.json.
func backfillIcebergHandler(cat catalog.Catalog, snap r2Snapshotter, defaultMinQuality float64) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		qs := req.URL.Query()

		minQuality := defaultMinQuality
		if v := qs.Get("min_quality"); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
				minQuality = f
			}
		}
		var sinceFilter *time.Time
		if v := qs.Get("since"); v != "" {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				sinceFilter = &t
			} else if t, err := time.Parse("2006-01-02", v); err == nil {
				sinceFilter = &t
			}
		}
		triggerDeploy := qs.Get("trigger_deploy") != "false"

		tbl, err := cat.LoadTable(ctx, []string{"jobs", "canonicals_current"})
		if err != nil {
			http.Error(w, "load table: "+err.Error(), http.StatusBadGateway)
			return
		}

		// Build filter: status='active' AND quality_score >= minQuality.
		var filter iceberg.BooleanExpression = iceberg.NewAnd(
			iceberg.EqualTo(iceberg.Reference("status"), "active"),
			iceberg.GreaterThanEqual(iceberg.Reference("quality_score"), minQuality),
		)
		if sinceFilter != nil {
			filter = iceberg.NewAnd(
				filter,
				iceberg.GreaterThanEqual(iceberg.Reference("posted_at"), sinceFilter.UnixMicro()),
			)
		}

		scan := tbl.Scan(table.WithRowFilter(filter))

		// Stream NDJSON progress output.
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)

		var total, uploaded, skipped int

		// Use ToArrowTable to collect rows. At ~10M canonicals target,
		// switch to ToArrowRecords batch iteration if this OOMs. For v1
		// scale (<1M canonicals) ToArrowTable is comfortably within limits.
		arrowTbl, err := scan.ToArrowTable(ctx)
		if err != nil {
			http.Error(w, "scan collect: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer arrowTbl.Release()

		rows, err := canonicalRowsFromTable(arrowTbl)
		if err != nil {
			http.Error(w, "decode rows: "+err.Error(), http.StatusBadGateway)
			return
		}

		for _, row := range rows {
			total++
			if row.Slug == "" {
				skipped++
				continue
			}
			snapJSON, err := json.Marshal(buildHugoDoc(row))
			if err != nil {
				skipped++
				continue
			}
			if err := snap.UploadPublicSnapshot(ctx, "jobs/"+row.Slug+".json", snapJSON); err != nil {
				util.Log(ctx).WithError(err).WithField("slug", row.Slug).Warn("backfill: upload failed")
				skipped++
				continue
			}
			uploaded++
			if uploaded%100 == 0 {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"progress": true, "uploaded": uploaded, "skipped": skipped,
				})
				if flusher != nil {
					flusher.Flush()
				}
			}
		}

		if triggerDeploy && uploaded > 0 {
			if err := snap.TriggerDeploy(); err != nil {
				util.Log(ctx).WithError(err).Warn("backfill: deploy hook failed")
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"done": true, "total": total, "uploaded": uploaded, "skipped": skipped,
			"deployed": triggerDeploy && uploaded > 0,
		})
	}
}

// canonicalRow carries the fields extracted from one row of the
// jobs.canonicals_current Iceberg table.
type canonicalRow struct {
	CanonicalID    string
	Slug           string
	Title          string
	Company        string
	Description    string
	LocationText   string
	Country        string
	Language       string
	RemoteType     string
	EmploymentType string
	Seniority      string
	Category       string
	Currency       string
	SalaryMin      float64
	SalaryMax      float64
	QualityScore   float64
	PostedAt       time.Time
}

// canonicalRowsFromTable decodes an Arrow table with the
// jobs.canonicals_current schema into []canonicalRow.
// Columns are resolved by name so schema evolution (added fields) is safe.
func canonicalRowsFromTable(tbl arrow.Table) ([]canonicalRow, error) {
	sc := tbl.Schema()
	if len(sc.FieldIndices("canonical_id")) == 0 {
		return nil, fmt.Errorf("canonicals_current: missing required column canonical_id; schema: %s", sc)
	}

	tr := array.NewTableReader(tbl, -1)
	defer tr.Release()

	var out []canonicalRow
	for tr.Next() {
		rec := tr.Record()
		nRows := int(rec.NumRows())

		colCanonicalID := stringColByNameRec(rec, "canonical_id")
		colSlug := stringColByNameRec(rec, "slug")
		colTitle := stringColByNameRec(rec, "title")
		colCompany := stringColByNameRec(rec, "company")
		colDescription := stringColByNameRec(rec, "description")
		colLocationText := stringColByNameRec(rec, "location_text")
		colCountry := stringColByNameRec(rec, "country")
		colLanguage := stringColByNameRec(rec, "language")
		colRemoteType := stringColByNameRec(rec, "remote_type")
		colEmploymentType := stringColByNameRec(rec, "employment_type")
		colSeniority := stringColByNameRec(rec, "seniority")
		colCategory := stringColByNameRec(rec, "category")
		colCurrency := stringColByNameRec(rec, "currency")
		colSalaryMin := float64ColByNameRec(rec, "salary_min")
		colSalaryMax := float64ColByNameRec(rec, "salary_max")
		colQualityScore := float64ColByNameRec(rec, "quality_score")
		colPostedAt := timestampColByNameRec(rec, "posted_at")

		for i := 0; i < nRows; i++ {
			row := canonicalRow{}
			if colCanonicalID != nil && !colCanonicalID.IsNull(i) {
				row.CanonicalID = colCanonicalID.Value(i)
			}
			if colSlug != nil && !colSlug.IsNull(i) {
				row.Slug = colSlug.Value(i)
			}
			if colTitle != nil && !colTitle.IsNull(i) {
				row.Title = colTitle.Value(i)
			}
			if colCompany != nil && !colCompany.IsNull(i) {
				row.Company = colCompany.Value(i)
			}
			if colDescription != nil && !colDescription.IsNull(i) {
				row.Description = colDescription.Value(i)
			}
			if colLocationText != nil && !colLocationText.IsNull(i) {
				row.LocationText = colLocationText.Value(i)
			}
			if colCountry != nil && !colCountry.IsNull(i) {
				row.Country = colCountry.Value(i)
			}
			if colLanguage != nil && !colLanguage.IsNull(i) {
				row.Language = colLanguage.Value(i)
			}
			if colRemoteType != nil && !colRemoteType.IsNull(i) {
				row.RemoteType = colRemoteType.Value(i)
			}
			if colEmploymentType != nil && !colEmploymentType.IsNull(i) {
				row.EmploymentType = colEmploymentType.Value(i)
			}
			if colSeniority != nil && !colSeniority.IsNull(i) {
				row.Seniority = colSeniority.Value(i)
			}
			if colCategory != nil && !colCategory.IsNull(i) {
				row.Category = colCategory.Value(i)
			}
			if colCurrency != nil && !colCurrency.IsNull(i) {
				row.Currency = colCurrency.Value(i)
			}
			if colSalaryMin != nil && !colSalaryMin.IsNull(i) {
				row.SalaryMin = colSalaryMin.Value(i)
			}
			if colSalaryMax != nil && !colSalaryMax.IsNull(i) {
				row.SalaryMax = colSalaryMax.Value(i)
			}
			if colQualityScore != nil && !colQualityScore.IsNull(i) {
				row.QualityScore = colQualityScore.Value(i)
			}
			if colPostedAt != nil && !colPostedAt.IsNull(i) {
				row.PostedAt = arrowTimestampToTime(rec, "posted_at", colPostedAt, i)
			}
			out = append(out, row)
		}
	}
	return out, nil
}

// buildHugoDoc serialises a canonicalRow into the shape Hugo consumes.
func buildHugoDoc(r canonicalRow) map[string]any {
	return map[string]any{
		"canonical_id":    r.CanonicalID,
		"slug":            r.Slug,
		"title":           r.Title,
		"company":         r.Company,
		"description":     r.Description,
		"location_text":   r.LocationText,
		"country":         r.Country,
		"language":        r.Language,
		"remote_type":     r.RemoteType,
		"employment_type": r.EmploymentType,
		"seniority":       r.Seniority,
		"category":        r.Category,
		"salary_min":      r.SalaryMin,
		"salary_max":      r.SalaryMax,
		"currency":        r.Currency,
		"posted_at":       r.PostedAt,
		"quality_score":   r.QualityScore,
	}
}

// --- Arrow column helpers (inlined; shared with worker via candidatestore pattern) ---

func stringColByNameRec(rec arrow.Record, name string) *array.String {
	idxs := rec.Schema().FieldIndices(name)
	if len(idxs) == 0 || idxs[0] < 0 || idxs[0] >= int(rec.NumCols()) {
		return nil
	}
	c, _ := rec.Column(idxs[0]).(*array.String)
	return c
}

func float64ColByNameRec(rec arrow.Record, name string) *array.Float64 {
	idxs := rec.Schema().FieldIndices(name)
	if len(idxs) == 0 || idxs[0] < 0 || idxs[0] >= int(rec.NumCols()) {
		return nil
	}
	c, _ := rec.Column(idxs[0]).(*array.Float64)
	return c
}

func timestampColByNameRec(rec arrow.Record, name string) *array.Timestamp {
	idxs := rec.Schema().FieldIndices(name)
	if len(idxs) == 0 || idxs[0] < 0 || idxs[0] >= int(rec.NumCols()) {
		return nil
	}
	c, _ := rec.Column(idxs[0]).(*array.Timestamp)
	return c
}

// arrowTimestampToTime converts one Timestamp cell to time.Time, respecting
// the column's TimeUnit. Mirrors the pattern in pkg/candidatestore/stale_reader.go.
func arrowTimestampToTime(rec arrow.Record, colName string, col *array.Timestamp, i int) time.Time {
	ts := col.Value(i)
	idxs := rec.Schema().FieldIndices(colName)
	if len(idxs) > 0 {
		field := rec.Schema().Field(idxs[0])
		if tsType, ok := field.Type.(*arrow.TimestampType); ok {
			if fn, err := tsType.GetToTimeFunc(); err == nil {
				return fn(ts)
			}
			return ts.ToTime(tsType.Unit)
		}
	}
	// Fallback: Iceberg default is microseconds.
	return ts.ToTime(arrow.Microsecond)
}
