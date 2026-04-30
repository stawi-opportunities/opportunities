package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// AutoApplyRepo is the narrow repository interface the auto-apply
// preference endpoints need.
type AutoApplyRepo interface {
	UpdateAutoApply(ctx context.Context, candidateID string, enabled bool) error
}

// AutoApplyEnableHandler returns an http.HandlerFunc for:
//
//	POST /candidates/{id}/auto-apply/enable
//
// Sets auto_apply=true for the candidate. Requires a paid subscription;
// enforcement is done on the auto-apply trigger side, not here — this
// handler is a simple flag setter so the UI can toggle the feature.
func AutoApplyEnableHandler(repo AutoApplyRepo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			http.Error(w, `{"error":"candidate id required"}`, http.StatusBadRequest)
			return
		}
		if err := repo.UpdateAutoApply(r.Context(), id, true); err != nil {
			http.Error(w, `{"error":"update failed"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"auto_apply": true, "candidate_id": id})
	}
}

// AutoApplyDisableHandler returns an http.HandlerFunc for:
//
//	POST /candidates/{id}/auto-apply/disable
func AutoApplyDisableHandler(repo AutoApplyRepo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			http.Error(w, `{"error":"candidate id required"}`, http.StatusBadRequest)
			return
		}
		if err := repo.UpdateAutoApply(r.Context(), id, false); err != nil {
			http.Error(w, `{"error":"update failed"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"auto_apply": false, "candidate_id": id})
	}
}
