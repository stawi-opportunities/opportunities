package v1

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

// StarUnstarStore is the write surface SavedJobsHandler depends on.
// Implemented by *savedjobs.Store in production; the interface lets
// tests pass an in-memory fake.
type StarUnstarStore interface {
	Star(ctx context.Context, candidateID, opportunityID string) error
	Unstar(ctx context.Context, candidateID, opportunityID string) error
}

// SavedJobsDeps bundles the inputs the handler needs.
type SavedJobsDeps struct {
	Store StarUnstarStore
}

// SavedJobsHandler dispatches POST (star) and DELETE (unstar) on the
// same path. POST takes a JSON body `{opportunity_id}` so the route
// `/me/saved-jobs` doesn't need a path param; DELETE pulls the id
// from the path `/me/saved-jobs/{opportunity_id}` so REST clients can
// hit it idempotently from a URL alone.
//
// Mount with two registrations:
//
//	mux.Handle("POST   /me/saved-jobs",                  httpmw.CandidateAuth(handler))
//	mux.Handle("DELETE /me/saved-jobs/{opportunity_id}", httpmw.CandidateAuth(handler))
func SavedJobsHandler(deps SavedJobsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			savedJobsStar(deps, w, r)
		case http.MethodDelete:
			savedJobsUnstar(deps, w, r)
		default:
			w.Header().Set("Allow", "POST, DELETE")
			httpmw.ProblemJSON(w, http.StatusMethodNotAllowed,
				"method_not_allowed", "use POST to star, DELETE /{opportunity_id} to unstar")
		}
	}
}

func savedJobsStar(deps SavedJobsDeps, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := util.Log(ctx)
	candidateID := httpmw.CandidateFromContext(ctx)

	body, err := io.ReadAll(io.LimitReader(r.Body, 4*1024))
	if err != nil {
		httpmw.ProblemJSON(w, http.StatusBadRequest, "body_read_failed", "could not read request body")
		return
	}
	var in struct {
		OpportunityID string `json:"opportunity_id"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		httpmw.ProblemJSON(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
		return
	}
	oppID := strings.TrimSpace(in.OpportunityID)
	if oppID == "" {
		httpmw.ProblemJSON(w, http.StatusBadRequest, "opportunity_id_required", "opportunity_id is required")
		return
	}
	if err := deps.Store.Star(ctx, candidateID, oppID); err != nil {
		log.WithError(err).WithField("candidate_id", candidateID).WithField("opportunity_id", oppID).
			Error("me/saved-jobs: star failed")
		httpmw.ProblemJSON(w, http.StatusBadGateway, "star_failed", "could not save opportunity")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func savedJobsUnstar(deps SavedJobsDeps, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := util.Log(ctx)
	candidateID := httpmw.CandidateFromContext(ctx)

	oppID := strings.TrimSpace(r.PathValue("opportunity_id"))
	if oppID == "" {
		httpmw.ProblemJSON(w, http.StatusBadRequest, "opportunity_id_required", "opportunity_id is required in the path")
		return
	}
	if err := deps.Store.Unstar(ctx, candidateID, oppID); err != nil {
		log.WithError(err).WithField("candidate_id", candidateID).WithField("opportunity_id", oppID).
			Error("me/saved-jobs: unstar failed")
		httpmw.ProblemJSON(w, http.StatusBadGateway, "unstar_failed", "could not remove opportunity")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
