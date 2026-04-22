package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/iceberg-go/catalog"
	"github.com/pitabwire/util"
	"github.com/redis/go-redis/v9"

	"stawi.jobs/pkg/kv"
)

// KVRebuilder repopulates Valkey cluster:* keys from the Iceberg
// jobs.canonicals_current table. Called after a Valkey replica loss.
//
// Scope: cluster:{cluster_id} := JSON snapshot only. dedup:{hard_key}
// rebuild requires variant-level data and is out of scope; the first
// variant through the pipeline after a rebuild creates a fresh cluster,
// and hourly/daily compaction re-merges any duplicates detected by
// hard_key overlap.
type KVRebuilder struct {
	cat catalog.Catalog
	kv  *redis.Client
}

// NewKVRebuilder constructs a KVRebuilder.
func NewKVRebuilder(cat catalog.Catalog, kv *redis.Client) *KVRebuilder {
	return &KVRebuilder{cat: cat, kv: kv}
}

// KVRebuildResult holds counters reported by a single rebuild run.
type KVRebuildResult struct {
	Rows           int `json:"rows"`
	ClusterKeysSet int `json:"cluster_keys_set"`
}

// Run scans jobs.canonicals_current via Iceberg and writes a
// cluster:{cluster_id} key for every row with a non-empty cluster_id.
// Rows are pipelined to Valkey in batches of 500.
func (r *KVRebuilder) Run(ctx context.Context) (KVRebuildResult, error) {
	var res KVRebuildResult

	tbl, err := r.cat.LoadTable(ctx, []string{"jobs", "canonicals_current"})
	if err != nil {
		return res, fmt.Errorf("kv rebuild: load table: %w", err)
	}

	scan := tbl.Scan()

	atbl, err := scan.ToArrowTable(ctx)
	if err != nil {
		return res, fmt.Errorf("kv rebuild: to arrow: %w", err)
	}
	defer atbl.Release()

	rows, err := canonicalMinimalRowsFromTable(atbl)
	if err != nil {
		return res, fmt.Errorf("kv rebuild: decode rows: %w", err)
	}

	pipe := r.kv.Pipeline()
	batchSize := 0
	for _, row := range rows {
		res.Rows++
		if row.ClusterID == "" {
			continue
		}

		snap, merr := json.Marshal(clusterSnapshotFromMinimal(row))
		if merr != nil {
			continue
		}
		// TTL 0 = no expiry — cluster snapshots are permanent.
		pipe.Set(ctx, "cluster:"+row.ClusterID, snap, 0)
		res.ClusterKeysSet++
		batchSize++

		if batchSize >= 500 {
			if _, err := pipe.Exec(ctx); err != nil {
				return res, fmt.Errorf("kv rebuild: pipe exec: %w", err)
			}
			pipe = r.kv.Pipeline()
			batchSize = 0
		}
	}
	if batchSize > 0 {
		if _, err := pipe.Exec(ctx); err != nil {
			return res, fmt.Errorf("kv rebuild: pipe final: %w", err)
		}
	}

	util.Log(ctx).
		WithField("rows", res.Rows).
		WithField("cluster_keys", res.ClusterKeysSet).
		Info("kv rebuild complete")
	return res, nil
}

// canonicalMinimal carries the subset of canonicals_current fields needed
// for KV rebuild. Only cluster_id and the fields in kv.ClusterSnapshot are
// extracted — heavier columns (description, apply_url) are omitted from the
// scan selection by not being included in WithSelectedFields.
type canonicalMinimal struct {
	ClusterID      string
	CanonicalID    string
	Slug           string
	Title          string
	Company        string
	Country        string
	Language       string
	RemoteType     string
	EmploymentType string
	Seniority      string
	SalaryMin      float64
	SalaryMax      float64
	Currency       string
	Category       string
	QualityScore   float64
	Status         string
	FirstSeenAt    time.Time
	LastSeenAt     time.Time
	PostedAt       time.Time
	ApplyURL       string
}

// canonicalMinimalRowsFromTable decodes an Arrow table with (a subset of)
// the jobs.canonicals_current schema into []canonicalMinimal.
// Columns are resolved by name so missing columns degrade gracefully.
func canonicalMinimalRowsFromTable(tbl arrow.Table) ([]canonicalMinimal, error) {
	sc := tbl.Schema()
	if len(sc.FieldIndices("cluster_id")) == 0 && len(sc.FieldIndices("canonical_id")) == 0 {
		return nil, fmt.Errorf("canonicals_current: missing both cluster_id and canonical_id; schema: %s", sc)
	}

	tr := array.NewTableReader(tbl, -1)
	defer tr.Release()

	var out []canonicalMinimal
	for tr.Next() {
		rec := tr.Record()
		nRows := int(rec.NumRows())

		colClusterID := kvStringCol(rec, "cluster_id")
		colCanonicalID := kvStringCol(rec, "canonical_id")
		colSlug := kvStringCol(rec, "slug")
		colTitle := kvStringCol(rec, "title")
		colCompany := kvStringCol(rec, "company")
		colCountry := kvStringCol(rec, "country")
		colLanguage := kvStringCol(rec, "language")
		colRemoteType := kvStringCol(rec, "remote_type")
		colEmploymentType := kvStringCol(rec, "employment_type")
		colSeniority := kvStringCol(rec, "seniority")
		colCurrency := kvStringCol(rec, "currency")
		colCategory := kvStringCol(rec, "category")
		colStatus := kvStringCol(rec, "status")
		colApplyURL := kvStringCol(rec, "apply_url")
		colSalaryMin := kvFloat64Col(rec, "salary_min")
		colSalaryMax := kvFloat64Col(rec, "salary_max")
		colQualityScore := kvFloat64Col(rec, "quality_score")
		colFirstSeenAt := kvTimestampCol(rec, "first_seen_at")
		colLastSeenAt := kvTimestampCol(rec, "last_seen_at")
		colPostedAt := kvTimestampCol(rec, "posted_at")

		for i := 0; i < nRows; i++ {
			row := canonicalMinimal{}
			if colClusterID != nil && !colClusterID.IsNull(i) {
				row.ClusterID = colClusterID.Value(i)
			}
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
			if colCurrency != nil && !colCurrency.IsNull(i) {
				row.Currency = colCurrency.Value(i)
			}
			if colCategory != nil && !colCategory.IsNull(i) {
				row.Category = colCategory.Value(i)
			}
			if colStatus != nil && !colStatus.IsNull(i) {
				row.Status = colStatus.Value(i)
			}
			if colApplyURL != nil && !colApplyURL.IsNull(i) {
				row.ApplyURL = colApplyURL.Value(i)
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
			if colFirstSeenAt != nil && !colFirstSeenAt.IsNull(i) {
				row.FirstSeenAt = kvTimestampToTime(rec, "first_seen_at", colFirstSeenAt, i)
			}
			if colLastSeenAt != nil && !colLastSeenAt.IsNull(i) {
				row.LastSeenAt = kvTimestampToTime(rec, "last_seen_at", colLastSeenAt, i)
			}
			if colPostedAt != nil && !colPostedAt.IsNull(i) {
				row.PostedAt = kvTimestampToTime(rec, "posted_at", colPostedAt, i)
			}
			out = append(out, row)
		}
	}
	return out, nil
}

// clusterSnapshotFromMinimal maps a canonicalMinimal to the kv.ClusterSnapshot
// shape that the canonical-merge handler reads on the hot path.
func clusterSnapshotFromMinimal(row canonicalMinimal) kv.ClusterSnapshot {
	return kv.ClusterSnapshot{
		ClusterID:      row.ClusterID,
		CanonicalID:    row.CanonicalID,
		Slug:           row.Slug,
		Title:          row.Title,
		Company:        row.Company,
		Country:        row.Country,
		Language:       row.Language,
		RemoteType:     row.RemoteType,
		EmploymentType: row.EmploymentType,
		Seniority:      row.Seniority,
		SalaryMin:      row.SalaryMin,
		SalaryMax:      row.SalaryMax,
		Currency:       row.Currency,
		Category:       row.Category,
		QualityScore:   row.QualityScore,
		Status:         row.Status,
		FirstSeenAt:    row.FirstSeenAt,
		LastSeenAt:     row.LastSeenAt,
		PostedAt:       row.PostedAt,
		ApplyURL:       row.ApplyURL,
	}
}

// --- Arrow column helpers (local to kv_rebuild; mirrors candidatestore pattern) ---

func kvStringCol(rec arrow.Record, name string) *array.String {
	idxs := rec.Schema().FieldIndices(name)
	if len(idxs) == 0 || idxs[0] < 0 || idxs[0] >= int(rec.NumCols()) {
		return nil
	}
	c, _ := rec.Column(idxs[0]).(*array.String)
	return c
}

func kvFloat64Col(rec arrow.Record, name string) *array.Float64 {
	idxs := rec.Schema().FieldIndices(name)
	if len(idxs) == 0 || idxs[0] < 0 || idxs[0] >= int(rec.NumCols()) {
		return nil
	}
	c, _ := rec.Column(idxs[0]).(*array.Float64)
	return c
}

func kvTimestampCol(rec arrow.Record, name string) *array.Timestamp {
	idxs := rec.Schema().FieldIndices(name)
	if len(idxs) == 0 || idxs[0] < 0 || idxs[0] >= int(rec.NumCols()) {
		return nil
	}
	c, _ := rec.Column(idxs[0]).(*array.Timestamp)
	return c
}

// kvTimestampToTime converts one Timestamp cell to time.Time,
// respecting the column TimeUnit. Mirrors stale_reader.go's helper.
func kvTimestampToTime(rec arrow.Record, colName string, col *array.Timestamp, i int) time.Time {
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
