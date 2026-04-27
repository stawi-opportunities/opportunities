package v1

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

// PreferencesHandler returns an http.HandlerFunc for:
//
//	POST /candidates/preferences
//	Content-Type: application/json
//	Body: { "candidate_id": "...", "opt_ins": { "<kind>": {...kind-prefs...}, ... } }
//
// The body is a replace-all snapshot of the candidate's per-kind
// preferences. Each opt-in is an arbitrary JSON object; the matcher
// for the corresponding kind interprets it. The handler validates the
// candidate_id and the structural shape only — kind-specific validation
// lives in each matcher.
func PreferencesHandler(svc *frame.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := util.Log(ctx)

		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		var body eventsv1.PreferencesUpdatedV1
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
			return
		}
		body.CandidateID = strings.TrimSpace(body.CandidateID)
		if body.CandidateID == "" {
			http.Error(w, `{"error":"candidate_id is required"}`, http.StatusBadRequest)
			return
		}
		if body.UpdatedAt.IsZero() {
			body.UpdatedAt = time.Now().UTC()
		}

		env := eventsv1.NewEnvelope(eventsv1.TopicCandidatePreferencesUpdated, body)
		if err := svc.EventsManager().Emit(ctx, eventsv1.TopicCandidatePreferencesUpdated, env); err != nil {
			log.WithError(err).Error("preferences: emit failed")
			http.Error(w, `{"error":"emit failed"}`, http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accepted":     true,
			"candidate_id": body.CandidateID,
		})
	}
}
