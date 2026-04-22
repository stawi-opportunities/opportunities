package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/pitabwire/util"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/eventlog"
)

// r2Snapshotter is the minimal publish interface the backfill needs.
type r2Snapshotter interface {
	UploadPublicSnapshot(ctx context.Context, key string, body []byte) error
	TriggerDeploy() error
}

// backfillParquetHandler walks canonicals_current/ in R2 and publishes
// Hugo snapshots under jobs/<slug>.json.
func backfillParquetHandler(reader *eventlog.Reader, snap r2Snapshotter, defaultMinQuality float64) http.HandlerFunc {
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

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)

		var total, uploaded, skipped int
		cursor := ""
		for {
			page, err := reader.ListNewObjects(ctx, "canonicals_current/", cursor, 500)
			if err != nil {
				util.Log(ctx).WithError(err).Error("backfill: list failed")
				_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}
			if len(page) == 0 {
				break
			}
			for _, o := range page {
				if o.Key == nil {
					continue
				}
				body, err := reader.Get(ctx, *o.Key)
				if err != nil {
					skipped++
					continue
				}
				rows, err := eventlog.ReadParquet[eventsv1.CanonicalUpsertedV1](body)
				if err != nil {
					skipped++
					continue
				}
				for _, r := range rows {
					total++
					if r.Status != "active" {
						continue
					}
					if r.QualityScore < minQuality {
						continue
					}
					if sinceFilter != nil && !r.PostedAt.IsZero() && r.PostedAt.Before(*sinceFilter) {
						continue
					}
					if r.Slug == "" {
						continue
					}

					snapJSON, err := buildHugoSnapshot(r)
					if err != nil {
						skipped++
						continue
					}
					key := "jobs/" + r.Slug + ".json"
					if err := snap.UploadPublicSnapshot(ctx, key, snapJSON); err != nil {
						util.Log(ctx).WithError(err).WithField("r2_key", key).Warn("backfill: upload failed")
						skipped++
						continue
					}
					uploaded++
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"progress": true, "uploaded": uploaded, "skipped": skipped,
				})
				if flusher != nil {
					flusher.Flush()
				}
			}
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

// buildHugoSnapshot serialises a CanonicalUpsertedV1 into the shape
// Hugo consumes. Keeps the field set the existing Hugo templates expect.
func buildHugoSnapshot(r eventsv1.CanonicalUpsertedV1) ([]byte, error) {
	return json.Marshal(map[string]any{
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
	})
}
