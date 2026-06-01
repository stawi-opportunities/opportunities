// apps/api/cmd/checkpoint_admin.go
//
// Admin /admin/checkpoints surface — inspect and clear the per-source
// iterator checkpoints that drive resume-after-crash on the crawler.
//
//   GET    /admin/checkpoints[?source_id=…]
//   DELETE /admin/checkpoints/{source_id}/{connector_type}
//
// Operator workflow: a wedged crawl that keeps resuming from page N
// (because the listing page has shifted and we're no longer emitting
// new variants) can be force-restarted by clearing the checkpoint.
// The next scheduled crawl tick then walks the source from page 1.
//
// Read endpoint also powers the "Reset checkpoint" button on the
// SourceTrace UI page.
package main

import (
	"net/http"

	"github.com/stawi-opportunities/opportunities/pkg/repository"
)

// checkpointAdmin bundles the deps for the /admin/checkpoints
// handlers. The repo is the only collaborator — schema lives in
// crawl_checkpoints, Postgres-only, no external storage.
type checkpointAdmin struct {
	repo *repository.CheckpointRepository
}

// registerCheckpointAdmin wires the GET/DELETE pair on mux. Called
// from sources_admin.go alongside registerTraceAdmin so all the
// trace+resume admin surface boots together.
func registerCheckpointAdmin(mux *http.ServeMux, repo *repository.CheckpointRepository) {
	a := &checkpointAdmin{repo: repo}
	mux.HandleFunc("GET /admin/checkpoints", requireAdmin(a.list))
	mux.HandleFunc("DELETE /admin/checkpoints/{source_id}/{connector_type}", requireAdmin(a.clear))
}

// list answers GET /admin/checkpoints[?source_id=…]. Without
// source_id returns every checkpoint row (typically O(active sources)
// — small). With source_id, filters to that source's rows. The
// repo's Cursor field is json.RawMessage so callers see the
// connector-native shape (page+after-token+whatever).
func (a *checkpointAdmin) list(w http.ResponseWriter, r *http.Request) {
	sourceID := r.URL.Query().Get("source_id")
	var rows []repository.Checkpoint
	var err error
	if sourceID != "" {
		rows, err = a.repo.ListBySource(r.Context(), sourceID)
	} else {
		rows, err = a.repo.ListAll(r.Context())
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"checkpoints": rows,
		"count":       len(rows),
	})
}

// clear answers DELETE /admin/checkpoints/{source_id}/{connector_type}.
// Idempotent — clearing a non-existent row succeeds (204).
func (a *checkpointAdmin) clear(w http.ResponseWriter, r *http.Request) {
	sourceID := r.PathValue("source_id")
	connectorType := r.PathValue("connector_type")
	if sourceID == "" || connectorType == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "source_id and connector_type required")
		return
	}
	if err := a.repo.Clear(r.Context(), sourceID, connectorType); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	logAction(r, "checkpoint_clear", sourceID)
	w.WriteHeader(http.StatusNoContent)
}
