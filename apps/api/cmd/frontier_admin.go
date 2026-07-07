// apps/api/cmd/frontier_admin.go
//
// Admin /admin/frontier surface — inspect, requeue, and delete
// rows in the D2 URL frontier.
//
//	GET    /admin/frontier[?state=…&host=…&source_id=…&limit=&offset=]
//	GET    /admin/frontier/{url_id}
//	POST   /admin/frontier/{url_id}/requeue
//	DELETE /admin/frontier/{url_id}
//
// Operator workflow: a host backlog or a wedged URL with retries
// exhausted can be triaged by listing failed rows for that host,
// requeueing the ones worth retrying, and hard-deleting the
// permanently-stuck ones (e.g. dead permalinks).
package main

import (
	"net/http"
	"strconv"

	"github.com/stawi-opportunities/opportunities/pkg/frontier"
)

// frontierAdmin bundles the deps for /admin/frontier handlers.
type frontierAdmin struct {
	repo *frontier.AdminRepository
}

// registerFrontierAdmin wires the frontier admin endpoints.
func registerFrontierAdmin(mux *http.ServeMux, repo *frontier.AdminRepository) {
	a := &frontierAdmin{repo: repo}
	mux.HandleFunc("GET /admin/frontier", requireAdmin(a.list))
	mux.HandleFunc("GET /admin/frontier/{url_id}", requireAdmin(a.get))
	mux.HandleFunc("POST /admin/frontier/{url_id}/requeue", requireAdmin(a.requeue))
	mux.HandleFunc("DELETE /admin/frontier/{url_id}", requireAdmin(a.delete))
}

// list answers GET /admin/frontier with filtering by state, host,
// and source_id. Pagination by offset+limit; cursor-by-priority
// would be nicer but offset is fine at the expected row counts
// (a busy frontier is ~10⁵ pending; offset over the full table
// stays sub-second).
func (a *frontierAdmin) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))

	f := frontier.AdminFilter{
		State:    frontier.State(q.Get("state")),
		Host:     q.Get("host"),
		SourceID: q.Get("source_id"),
		Limit:    limit,
		Offset:   offset,
	}

	rows, total, err := a.repo.List(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"frontier": rows,
		"total":    total,
		"limit":    f.Limit,
		"offset":   f.Offset,
	})
}

// get answers GET /admin/frontier/{url_id}.
func (a *frontierAdmin) get(w http.ResponseWriter, r *http.Request) {
	urlID := r.PathValue("url_id")
	if urlID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "url_id required")
		return
	}
	row, err := a.repo.Get(r.Context(), urlID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_failed", err.Error())
		return
	}
	if row == nil {
		writeError(w, http.StatusNotFound, "not_found", "frontier row not found")
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// requeue answers POST /admin/frontier/{url_id}/requeue. Idempotent
// at the read-after-write level — running it twice on a pending row
// is a no-op.
func (a *frontierAdmin) requeue(w http.ResponseWriter, r *http.Request) {
	urlID := r.PathValue("url_id")
	if urlID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "url_id required")
		return
	}
	row, err := a.repo.Requeue(r.Context(), urlID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "requeue_failed", err.Error())
		return
	}
	if row == nil {
		writeError(w, http.StatusNotFound, "not_found", "frontier row not found")
		return
	}
	logAction(r, "frontier_requeue", urlID)
	writeJSON(w, http.StatusOK, row)
}

// delete answers DELETE /admin/frontier/{url_id}. Hard delete —
// the operator override for permanently-stuck rows.
func (a *frontierAdmin) delete(w http.ResponseWriter, r *http.Request) {
	urlID := r.PathValue("url_id")
	if urlID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "url_id required")
		return
	}
	if err := a.repo.Delete(r.Context(), urlID); err != nil {
		writeError(w, http.StatusInternalServerError, "delete_failed", err.Error())
		return
	}
	logAction(r, "frontier_delete", urlID)
	w.WriteHeader(http.StatusNoContent)
}
