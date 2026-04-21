package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pitabwire/util"
	"github.com/redis/go-redis/v9"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/eventlog"
	"stawi.jobs/pkg/kv"
)

// KVRebuilder repopulates Valkey cluster:* keys from R2
// canonicals_current/ Parquet. Called after a Valkey replica loss.
//
// Scope: cluster:{cluster_id} := JSON snapshot only. dedup:{hard_key}
// rebuild requires variant-level data and is out of scope; the first
// variant through the pipeline after a rebuild creates a fresh cluster,
// and hourly/daily compaction re-merges any duplicates detected by
// hard_key overlap.
type KVRebuilder struct {
	reader *eventlog.Reader
	kv     *redis.Client
}

// NewKVRebuilder constructs a KVRebuilder.
func NewKVRebuilder(reader *eventlog.Reader, kv *redis.Client) *KVRebuilder {
	return &KVRebuilder{reader: reader, kv: kv}
}

// KVRebuildResult holds counters reported by a single rebuild run.
type KVRebuildResult struct {
	Rows           int `json:"rows"`
	ClusterKeysSet int `json:"cluster_keys_set"`
	Files          int `json:"files_scanned"`
}

// Run pages through all objects under canonicals_current/ and writes
// a cluster:{cluster_id} key for every row with a non-empty cluster_id.
// It returns after all pages have been processed or on first error.
func (r *KVRebuilder) Run(ctx context.Context) (KVRebuildResult, error) {
	var res KVRebuildResult

	cursor := ""
	for {
		page, err := r.reader.ListNewObjects(ctx, "canonicals_current/", cursor, 1000)
		if err != nil {
			return res, fmt.Errorf("kv rebuild: list: %w", err)
		}
		if len(page) == 0 {
			break
		}

		for _, o := range page {
			if o.Key == nil {
				continue
			}
			body, err := r.reader.Get(ctx, *o.Key)
			if err != nil {
				return res, fmt.Errorf("kv rebuild: get %q: %w", *o.Key, err)
			}
			rows, err := eventlog.ReadParquet[eventsv1.CanonicalUpsertedV1](body)
			if err != nil {
				return res, fmt.Errorf("kv rebuild: parquet %q: %w", *o.Key, err)
			}
			res.Files++
			res.Rows += len(rows)

			pipe := r.kv.Pipeline()
			for _, row := range rows {
				if row.ClusterID == "" {
					continue
				}
				snap, merr := json.Marshal(snapshotFromCanonical(row))
				if merr != nil {
					continue
				}
				// TTL 0 = no expiry — cluster snapshots are permanent.
				pipe.Set(ctx, "cluster:"+row.ClusterID, snap, 0)
				res.ClusterKeysSet++
			}
			if _, err := pipe.Exec(ctx); err != nil {
				return res, fmt.Errorf("kv rebuild: pipe exec: %w", err)
			}
		}

		// Advance cursor to the last key seen in this page.
		lastKey := ""
		for i := len(page) - 1; i >= 0; i-- {
			if page[i].Key != nil {
				lastKey = *page[i].Key
				break
			}
		}
		if lastKey == "" {
			break
		}
		cursor = lastKey
	}

	util.Log(ctx).
		WithField("rows", res.Rows).
		WithField("cluster_keys", res.ClusterKeysSet).
		WithField("files", res.Files).
		Info("kv rebuild complete")
	return res, nil
}

// snapshotFromCanonical maps a CanonicalUpsertedV1 row to the
// kv.ClusterSnapshot shape that the canonical-merge handler reads
// on the hot path.
func snapshotFromCanonical(row eventsv1.CanonicalUpsertedV1) kv.ClusterSnapshot {
	return kv.ClusterSnapshot{
		ClusterID:    row.ClusterID,
		CanonicalID:  row.CanonicalID,
		Slug:         row.Slug,
		Title:        row.Title,
		Company:      row.Company,
		Country:      row.Country,
		Language:     row.Language,
		RemoteType:   row.RemoteType,
		QualityScore: row.QualityScore,
		Status:       row.Status,
		PostedAt:     row.PostedAt,
		FirstSeenAt:  row.FirstSeenAt,
		LastSeenAt:   row.LastSeenAt,
		ApplyURL:     row.ApplyURL,
	}
}
