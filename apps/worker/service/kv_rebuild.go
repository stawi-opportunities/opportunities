package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	iceberg "github.com/apache/iceberg-go"
	"github.com/apache/iceberg-go/catalog"
	"github.com/apache/iceberg-go/table"
	"github.com/pitabwire/util"
	"github.com/redis/go-redis/v9"

	"stawi.jobs/pkg/kv"
)

// KVRebuilder repopulates Valkey cluster:* keys from the append-only Iceberg
// jobs.canonicals table. Called after a Valkey replica loss.
//
// Because jobs.canonicals is append-only (each canonical update appends a new
// row), the rebuilder folds all rows for a given cluster_id in Go, keeping
// the row with the latest occurred_at as the authoritative snapshot.
//
// Scope: cluster:{cluster_id} := JSON snapshot only. dedup:{hard_key}
// rebuild requires variant-level data and is out of scope; the first variant
// through the pipeline after a rebuild creates a fresh cluster, and
// hourly/daily compaction re-merges any duplicates detected by hard_key overlap.
//
// Memory-safety at 500M scale: the table is iterated partition-by-partition
// (bucket(32, cluster_id)). Per partition ~15.6M rows × 200 bytes ≈ 3 GB peak,
// well within the 4 GB worker pod limit. After each partition the in-memory map
// is flushed to Valkey and then cleared, so memory is reclaimed between
// partitions.
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

// Run scans the append-only jobs.canonicals table via Iceberg partition-by-
// partition, folds all rows per cluster_id keeping the latest by occurred_at,
// then writes a cluster:{cluster_id} key for every unique cluster_id.
//
// Each bucket partition is processed sequentially: rows are streamed via
// ToArrowRecords (lazy RecordBatch iterator), folded into a per-partition map,
// flushed to Valkey in batches of 500, then the map is cleared so memory is
// reclaimed before the next partition starts.
func (r *KVRebuilder) Run(ctx context.Context) (KVRebuildResult, error) {
	var res KVRebuildResult

	tbl, err := r.cat.LoadTable(ctx, []string{"jobs", "canonicals"})
	if err != nil {
		return res, fmt.Errorf("kv rebuild: load table: %w", err)
	}

	numBuckets, bucketFieldName, err := kvBucketPartitionInfo(tbl, "cluster_id")
	if err != nil {
		return res, fmt.Errorf("kv rebuild: partition info: %w", err)
	}

	for bucketIdx := 0; bucketIdx < numBuckets; bucketIdx++ {
		partFilter := iceberg.EqualTo(iceberg.Reference(bucketFieldName), int32(bucketIdx))

		scan := tbl.Scan(
			table.WithRowFilter(partFilter),
			table.WithSelectedFields(
				"canonical_id", "cluster_id", "slug", "title", "company",
				"country", "language", "remote_type", "employment_type", "seniority",
				"salary_min", "salary_max", "currency", "category",
				"quality_score", "status", "apply_url",
				"first_seen_at", "last_seen_at", "posted_at", "occurred_at",
			),
		)

		// Fold: per-partition latest-per-cluster_id map.
		latest := make(map[string]canonicalMinimal)

		_, itr, err := scan.ToArrowRecords(ctx)
		if err != nil {
			return res, fmt.Errorf("kv rebuild: to arrow records (bucket %d): %w", bucketIdx, err)
		}

		for batch, batchErr := range itr {
			if batchErr != nil {
				return res, fmt.Errorf("kv rebuild: iterate batch (bucket %d): %w", bucketIdx, batchErr)
			}
			rows, err := canonicalMinimalRowsFromRecord(batch)
			if err != nil {
				batch.Release()
				return res, fmt.Errorf("kv rebuild: decode rows (bucket %d): %w", bucketIdx, err)
			}
			res.Rows += len(rows)
			for _, row := range rows {
				if row.ClusterID == "" {
					continue
				}
				existing, ok := latest[row.ClusterID]
				if !ok || row.OccurredAt.After(existing.OccurredAt) {
					latest[row.ClusterID] = row
				}
			}
			batch.Release()
		}

		// Flush this partition's map to Valkey.
		flushed, err := r.flushToValkey(ctx, latest)
		if err != nil {
			return res, fmt.Errorf("kv rebuild: flush (bucket %d): %w", bucketIdx, err)
		}
		res.ClusterKeysSet += flushed

		util.Log(ctx).
			WithField("bucket", bucketIdx).
			WithField("cluster_keys", flushed).
			Debug("kv rebuild: bucket done")
	}

	util.Log(ctx).
		WithField("rows", res.Rows).
		WithField("cluster_keys", res.ClusterKeysSet).
		Info("kv rebuild complete")
	return res, nil
}

// flushToValkey pipelines SET commands for all entries in latest to Valkey,
// batching in groups of 500. Returns the number of keys written.
func (r *KVRebuilder) flushToValkey(ctx context.Context, latest map[string]canonicalMinimal) (int, error) {
	if len(latest) == 0 {
		return 0, nil
	}

	pipe := r.kv.Pipeline()
	batchSize := 0
	keysSet := 0

	for clusterID, row := range latest {
		snap, merr := json.Marshal(clusterSnapshotFromMinimal(row))
		if merr != nil {
			continue
		}
		// TTL 0 = no expiry — cluster snapshots are permanent.
		pipe.Set(ctx, "cluster:"+clusterID, snap, 0)
		keysSet++
		batchSize++

		if batchSize >= 500 {
			if _, err := pipe.Exec(ctx); err != nil {
				return keysSet - batchSize, fmt.Errorf("pipe exec: %w", err)
			}
			pipe = r.kv.Pipeline()
			batchSize = 0
		}
	}
	if batchSize > 0 {
		if _, err := pipe.Exec(ctx); err != nil {
			return keysSet - batchSize, fmt.Errorf("pipe final: %w", err)
		}
	}

	return keysSet, nil
}

// kvBucketPartitionInfo inspects the table's partition spec to find the bucket
// transform applied to sourceField (e.g. "cluster_id"). It returns the bucket
// count N and the partition field name used to filter scans
// (e.g. "cluster_id_bucket" or "cluster_id_bucket_32").
//
// The bucket transform exposes a computed partition field whose name is
// typically "<source>_bucket" or "<source>_bucket_<N>". We locate it by
// walking PartitionType.FieldList; the first field whose name has the source
// field as a prefix (and contains "bucket") is assumed to be the right one.
// If no bucket field is found, we return N=1 with an empty field name so the
// caller can fall back to a full-table scan.
func kvBucketPartitionInfo(tbl *table.Table, sourceField string) (int, string, error) {
	spec := tbl.Metadata().PartitionSpec()
	schema := tbl.Metadata().CurrentSchema()
	partType := spec.PartitionType(schema)

	for _, pf := range partType.FieldList {
		// Look for a partition field derived from sourceField via bucket transform.
		// Naming convention: "<source>_bucket" or "<source>_bucket_<N>".
		name := pf.Name
		if len(name) >= len(sourceField)+len("_bucket") &&
			name[:len(sourceField)] == sourceField &&
			containsSubstring(name[len(sourceField):], "bucket") {
			// Find the PartitionField in the spec to read N from the transform.
			for sf := range spec.Fields() {
				if sf.Name == name {
					if bt, ok := sf.Transform.(iceberg.BucketTransform); ok {
						return bt.NumBuckets, name, nil
					}
				}
			}
			// Bucket field found but transform not readable; default N=32.
			return 32, name, nil
		}
	}

	// No bucket partition on sourceField — treat as unpartitioned (N=1, no filter).
	return 1, "", nil
}

// containsSubstring is a simple substring check without importing strings.
func containsSubstring(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// canonicalMinimal carries the subset of canonicals fields needed for KV
// rebuild. OccurredAt is used for the in-Go fold (latest-per-cluster_id);
// it is not written to Valkey.
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
	OccurredAt     time.Time
	ApplyURL       string
}

// canonicalMinimalRowsFromRecord decodes a single Arrow RecordBatch with (a
// subset of) the jobs.canonicals schema into []canonicalMinimal. Columns are
// resolved by name so missing columns degrade gracefully.
func canonicalMinimalRowsFromRecord(rec arrow.Record) ([]canonicalMinimal, error) {
	sc := rec.Schema()
	if len(sc.FieldIndices("cluster_id")) == 0 && len(sc.FieldIndices("canonical_id")) == 0 {
		return nil, fmt.Errorf("canonicals: missing both cluster_id and canonical_id; schema: %s", sc)
	}

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
	colOccurredAt := kvTimestampCol(rec, "occurred_at")

	out := make([]canonicalMinimal, 0, nRows)
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
		if colOccurredAt != nil && !colOccurredAt.IsNull(i) {
			row.OccurredAt = kvTimestampToTime(rec, "occurred_at", colOccurredAt, i)
		}
		out = append(out, row)
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
