package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/pitabwire/util"
	"gorm.io/gorm"

	"stawi.jobs/pkg/locale"
)

// countryBackfillHandler backs POST /admin/backfill/country. Scans
// canonical_jobs rows where country='' and location_text is non-empty,
// runs locale.InferCountry over each, and persists the inferred code.
//
// The extractor upstream is supposed to populate country at canonical-
// build time, but historically it left the column empty on every job.
// This endpoint exists to repair that state without re-crawling —
// safe to re-run; rows already containing a country are untouched.
//
// Query params:
//
//	batch_size   — rows per fetch (default 500, max 5000)
//	dry_run=1    — compute inferences but don't write
func countryBackfillHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := util.Log(ctx)
		start := time.Now()

		batchSize := 500
		if v := r.URL.Query().Get("batch_size"); v != "" {
			if n, err := parseIntRange(v, 1, 5000); err == nil {
				batchSize = n
			}
		}
		dryRun := r.URL.Query().Get("dry_run") == "1"

		type row struct {
			ID           int64
			LocationText string
		}

		// Distinct by (inferred country → count) for the response
		// summary; useful to eyeball the spread without digging in
		// the DB.
		byCountry := map[string]int{}
		var scanned, updated, empty int

		// Paginate via id > lastID so we never skip or re-read rows.
		var lastID int64
		for {
			var rows []row
			q := db.WithContext(ctx).
				Table("canonical_jobs").
				Select("id, location_text").
				Where("status = 'active' AND (country = '' OR country IS NULL) AND location_text <> ''").
				Where("id > ?", lastID).
				Order("id ASC").
				Limit(batchSize)
			if err := q.Scan(&rows).Error; err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if len(rows) == 0 {
				break
			}

			updates := make(map[string][]int64, len(rows))
			for _, rr := range rows {
				scanned++
				cc := locale.InferCountry(rr.LocationText)
				if cc == "" {
					empty++
					byCountry[""]++
					continue
				}
				byCountry[cc]++
				updates[cc] = append(updates[cc], rr.ID)
			}

			if !dryRun && len(updates) > 0 {
				for cc, ids := range updates {
					if err := db.WithContext(ctx).
						Table("canonical_jobs").
						Where("id IN ? AND (country = '' OR country IS NULL)", ids).
						Update("country", cc).Error; err != nil {
						log.WithError(err).WithField("country", cc).
							Warn("country-backfill: update failed")
						continue
					}
					updated += len(ids)
				}
			}

			lastID = rows[len(rows)-1].ID
			if len(rows) < batchSize {
				break
			}
		}

		resp := map[string]any{
			"scanned":     scanned,
			"updated":     updated,
			"unresolved":  empty,
			"by_country":  byCountry,
			"dry_run":     dryRun,
			"duration_ms": time.Since(start).Milliseconds(),
		}
		log.WithField("scanned", scanned).
			WithField("updated", updated).
			WithField("unresolved", empty).
			Info("country-backfill done")

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// parseIntRange parses n and clamps into [lo, hi]. Returns 0/err on
// non-numeric input.
func parseIntRange(s string, lo, hi int) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errorString("not a number")
		}
		n = n*10 + int(c-'0')
		if n > hi {
			return hi, nil
		}
	}
	if n < lo {
		return lo, nil
	}
	return n, nil
}

type errorString string

func (e errorString) Error() string { return string(e) }
