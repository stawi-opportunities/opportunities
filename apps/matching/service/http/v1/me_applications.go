package v1

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

// ApplicationStarter is the slice of business logic this handler
// depends on. Defined here so the handler test can pass an in-memory
// fake without dragging the full business package + idempotency
// store into the test binary.
type ApplicationStarter interface {
	// StartApplication creates an `applications` row in the
	// submitted state for the given (candidate, opportunity, method)
	// triple. Returns the new application ID and the canonical
	// applied_at timestamp the dashboard will display.
	StartApplication(ctx context.Context, candidateID, opportunityID, method string) (applicationID string, appliedAt time.Time, err error)
}

type ApplicationsDeps struct {
	Starter ApplicationStarter
}

func ApplicationsHandler(deps ApplicationsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			httpmw.ProblemJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
			return
		}
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
			Method        string `json:"method"`
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
		method := strings.TrimSpace(in.Method)
		if method == "" {
			method = "manual"
		}

		appID, appliedAt, err := deps.Starter.StartApplication(ctx, candidateID, oppID, method)
		if err != nil {
			log.WithError(err).WithField("candidate_id", candidateID).WithField("opportunity_id", oppID).
				Error("me/applications: start failed")
			httpmw.ProblemJSON(w, http.StatusBadGateway, "apply_failed", "could not record application")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		// "applied" is the dashboard-facing label; storage uses submitted.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"application_id": appID,
			"status":         "applied",
			"applied_at":     appliedAt,
		})
	}
}
