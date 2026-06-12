// apps/api/cmd/crawl_run_admin.go
//
// Admin /admin/crawl-runs surface — inspect the crawl_runs state machine that
// drives resumable, bounded-slice crawling, and unstick a wedged run.
//
//	GET  /admin/crawl-runs[?source_id=…&limit=…]
//	POST /admin/crawl-runs/{source_id}/reset
//
// Operator workflow: a run that keeps lapsing without completing holds the
// source's single-flight slot (one active run per source). "Reset" fails the
// active run, freeing the slot so the next scheduled tick starts a fresh pass.
// The list endpoint powers the run view on the SourceTrace UI page.
package main

import (
	"net/http"
	"strconv"

	"github.com/stawi-opportunities/opportunities/pkg/repository"
)

// crawlRunAdmin bundles the deps for the /admin/crawl-runs handlers.
type crawlRunAdmin struct {
	repo *repository.CrawlRunRepository
}

// registerCrawlRunAdmin wires the GET/POST pair on mux. Called from
// sources_admin.go alongside the other trace+resume admin surface.
func registerCrawlRunAdmin(mux *http.ServeMux, repo *repository.CrawlRunRepository) {
	a := &crawlRunAdmin{repo: repo}
	mux.HandleFunc("GET /admin/crawl-runs", requireAdmin(a.list))
	mux.HandleFunc("POST /admin/crawl-runs/{source_id}/reset", requireAdmin(a.reset))
}

// list answers GET /admin/crawl-runs[?source_id=…&limit=…], newest first.
func (a *crawlRunAdmin) list(w http.ResponseWriter, r *http.Request) {
	sourceID := r.URL.Query().Get("source_id")
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	rows, err := a.repo.ListRuns(r.Context(), sourceID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": rows, "count": len(rows)})
}

// reset answers POST /admin/crawl-runs/{source_id}/reset — fails the source's
// active run so its single-flight slot frees up and the next tick starts fresh.
// Idempotent: no active run is a no-op success.
func (a *crawlRunAdmin) reset(w http.ResponseWriter, r *http.Request) {
	sourceID := r.PathValue("source_id")
	if sourceID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "source_id required")
		return
	}
	run, err := a.repo.GetActiveBySource(r.Context(), sourceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if run == nil {
		writeJSON(w, http.StatusOK, map[string]any{"reset": false, "reason": "no active run"})
		return
	}
	if err := a.repo.Fail(r.Context(), run.ID, "operator_reset", "reset via admin", 0, 0, 0); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	logAction(r, "crawl_run_reset", sourceID)
	writeJSON(w, http.StatusOK, map[string]any{"reset": true, "run_id": run.ID})
}
