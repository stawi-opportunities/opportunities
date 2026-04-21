package v1

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	eventsv1 "stawi.jobs/pkg/events/v1"
)

// PreferencesHandler returns an http.HandlerFunc for:
//
//	POST /candidates/preferences
//	Content-Type: application/json
//	Body: full preferences snapshot (replace-all semantics)
//
// The handler validates candidate_id + minimal sanity checks, then
// emits PreferencesUpdatedV1. Persistence happens via the event log.
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
		if body.SalaryMin < 0 || body.SalaryMax < 0 {
			http.Error(w, `{"error":"salary_min / salary_max must be non-negative"}`, http.StatusBadRequest)
			return
		}
		if body.SalaryMin > 0 && body.SalaryMax > 0 && body.SalaryMin > body.SalaryMax {
			http.Error(w, `{"error":"salary_min > salary_max"}`, http.StatusBadRequest)
			return
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
