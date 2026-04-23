package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/pitabwire/util"
)

// r2Snapshotter is the minimal publish interface the backfill needs.
type r2Snapshotter interface {
	UploadPublicSnapshot(ctx context.Context, key string, body []byte) error
	TriggerDeploy() error
}

// backfillManticoreHandler scans idx_jobs_rt in Manticore for active jobs
// above minQuality (and optionally posted after a since time) and publishes
// Hugo snapshots under jobs/<slug>.json.
//
// Pagination: pages of 500 rows via ScrollActive so the whole table can be
// walked without loading everything into memory at once.
func backfillManticoreHandler(jm *jobsManticore, snap r2Snapshotter, defaultMinQuality float64) http.HandlerFunc {
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

		// Stream NDJSON progress output.
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)

		var total, uploaded, skipped int

		err := jm.ScrollActive(ctx, minQuality, sinceFilter, 500, func(row job) error {
			total++
			if row.Slug == "" {
				skipped++
				return nil
			}
			snapJSON, merr := json.Marshal(buildHugoDocFromJob(row))
			if merr != nil {
				skipped++
				return nil
			}
			if uerr := snap.UploadPublicSnapshot(ctx, "jobs/"+row.Slug+".json", snapJSON); uerr != nil {
				util.Log(ctx).WithError(uerr).WithField("slug", row.Slug).Warn("backfill: upload failed")
				skipped++
				return nil
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
			return nil
		})
		if err != nil {
			http.Error(w, "backfill scroll: "+err.Error(), http.StatusBadGateway)
			return
		}

		if triggerDeploy && uploaded > 0 {
			if derr := snap.TriggerDeploy(); derr != nil {
				util.Log(ctx).WithError(derr).Warn("backfill: deploy hook failed")
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"done": true, "total": total, "uploaded": uploaded, "skipped": skipped,
			"deployed": triggerDeploy && uploaded > 0,
		})
	}
}

// ScrollActive iterates Manticore's idx_jobs_rt with status='active' and
// quality_score >= minScore, optionally further filtered by posted_at >= since.
// callback is called once per row; returning a non-nil error halts pagination.
// pageSize controls the LIMIT per request; 500 is a safe default.
func (j *jobsManticore) ScrollActive(
	ctx context.Context,
	minScore float64,
	since *time.Time,
	pageSize int,
	callback func(job) error,
) error {
	if pageSize <= 0 {
		pageSize = 500
	}

	filter := []map[string]any{
		{"equals": map[string]any{"status": "active"}},
	}
	if minScore > 0 {
		filter = append(filter, map[string]any{
			"range": map[string]any{
				"quality_score": map[string]any{"gte": minScore},
			},
		})
	}
	if since != nil {
		filter = append(filter, map[string]any{
			"range": map[string]any{
				"posted_at": map[string]any{"gte": since.Unix()},
			},
		})
	}

	for offset := 0; ; offset += pageSize {
		q := map[string]any{
			"index": "idx_jobs_rt",
			"query": map[string]any{"bool": map[string]any{"filter": filter}},
			"sort":  []any{map[string]any{"posted_at": "desc"}},
			"limit": pageSize,
			"offset": offset,
		}
		hits, total, err := j.search(ctx, q)
		if err != nil {
			return fmt.Errorf("scroll active (offset=%d): %w", offset, err)
		}
		for _, h := range hits {
			if err := callback(h); err != nil {
				return err
			}
		}
		// Stop when we've consumed all results or got an empty page.
		if len(hits) == 0 || offset+len(hits) >= total {
			break
		}
	}
	return nil
}

// buildHugoDocFromJob serialises a Manticore job row into the shape Hugo
// consumes.  The schema is identical to buildHugoDoc (Iceberg canonicalRow)
// because idx_jobs_rt is the materialised "latest canonical per cluster"
// index — same fields, different source.
func buildHugoDocFromJob(r job) map[string]any {
	doc := map[string]any{
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
		"currency":        r.Currency,
		"quality_score":   r.QualityScore,
	}
	if r.SalaryMin != nil {
		doc["salary_min"] = *r.SalaryMin
	} else {
		doc["salary_min"] = 0
	}
	if r.SalaryMax != nil {
		doc["salary_max"] = *r.SalaryMax
	} else {
		doc["salary_max"] = 0
	}
	if r.PostedAt != nil {
		doc["posted_at"] = *r.PostedAt
	} else {
		doc["posted_at"] = time.Time{}
	}
	return doc
}
