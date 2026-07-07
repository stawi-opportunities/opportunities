package service

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/stawi-opportunities/opportunities/pkg/repository"
)

// CrawlJobsAdminHandler returns the last N crawl_jobs for a given source,
// for operator triage.
func CrawlJobsAdminHandler(repo *repository.CrawlRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		sourceID := r.URL.Query().Get("source_id")
		if sourceID == "" {
			http.Error(w, "source_id required", http.StatusBadRequest)
			return
		}
		limit := 20
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
				limit = n
			}
		}
		rows, err := repo.ListBySource(ctx, sourceID, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"source_id": sourceID,
			"jobs":      rows,
		})
	}
}
