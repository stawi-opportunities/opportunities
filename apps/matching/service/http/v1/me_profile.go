package v1

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

// MeProfileResponse is the JSON shape returned by GET /me. Field names
// match ui/app/src/api/profile.ts:CandidateSummary so the frontend can
// pre-populate settings fields.
type MeProfileResponse struct {
	ProfileID          string `json:"profile_id"`
	Status             string `json:"status"`
	Name               string `json:"name"`
	CurrentTitle       string `json:"current_title"`
	Phone              string `json:"phone"`
	PreferredCountries string `json:"preferred_countries"`
	PreferredRegions   string `json:"preferred_regions"`
	RemotePreference   string `json:"remote_preference"`
	Languages          string `json:"languages"`
	PlanID             string `json:"plan_id"`
	Subscription       string `json:"subscription"`
}

// MeProfileUpdatePayload is the body accepted by PUT /me/profile.
// Fields match ui/app/src/api/profile.ts:ProfilePayload.
type MeProfileUpdatePayload struct {
	Name         string `json:"name"`
	CurrentTitle string `json:"current_title"`
	Phone        string `json:"phone"`
}

// CandidateProfileWriter is the subset of repository.CandidateRepository
// the PUT handler needs. Defined here as an interface so tests can use a
// fake without importing GORM.
type CandidateProfileWriter interface {
	GetByID(ctx context.Context, id string) (*domain.CandidateProfile, error)
	Update(ctx context.Context, c *domain.CandidateProfile) error
}

// ProfileDeps bundles the inputs ProfileHandler and ProfileUpdateHandler need.
type ProfileDeps struct {
	Candidates CandidateProfileWriter
}

// ProfileHandler returns the authenticated candidate's profile as
// {"candidate": {...}}. Returns null candidate on any lookup failure so
// the frontend can render an anon fallback.
//
//	GET /me
//	X-Candidate-ID: <candidate primary-key>
//	→ 200 {"candidate": {profile_id, status, current_title, ...}}
//	→ 502 {"candidate": null} on lookup failure
func ProfileHandler(deps ProfileDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := util.Log(ctx)
		candidateID := httpmw.CandidateFromContext(ctx)

		cand, err := deps.Candidates.GetByID(ctx, candidateID)
		if err != nil {
			log.WithError(err).WithField("candidate_id", candidateID).
				Error("me/profile: candidate lookup failed")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{"candidate": nil})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if cand == nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"candidate": nil})
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]any{"candidate": profileToResponse(cand)})
	}
}

// ProfileUpdateHandler updates the authenticated candidate's profile fields.
//
//	PUT /me/profile
//	X-Candidate-ID: <candidate primary-key>
//	Content-Type: application/json
//	{"name": "...", "current_title": "...", "phone": "..."}
//	→ 200 {"ok": true}
func ProfileUpdateHandler(deps ProfileDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := util.Log(ctx)
		candidateID := httpmw.CandidateFromContext(ctx)

		body, err := io.ReadAll(io.LimitReader(r.Body, 16*1024))
		if err != nil {
			httpmw.ProblemJSON(w, http.StatusBadRequest,
				"body_read_failed", "could not read request body")
			return
		}

		var in MeProfileUpdatePayload
		if err := json.Unmarshal(body, &in); err != nil {
			httpmw.ProblemJSON(w, http.StatusBadRequest,
				"invalid_json", "request body is not valid JSON")
			return
		}

		cand, err := deps.Candidates.GetByID(ctx, candidateID)
		if err != nil {
			log.WithError(err).WithField("candidate_id", candidateID).
				Error("me/profile: candidate lookup for update failed")
			httpmw.ProblemJSON(w, http.StatusBadGateway,
				"candidate_lookup_failed", "could not load candidate profile")
			return
		}
		if cand == nil {
			httpmw.ProblemJSON(w, http.StatusNotFound,
				"candidate_not_found", "no profile for this candidate")
			return
		}

		cand.Name = in.Name
		cand.CurrentTitle = in.CurrentTitle
		cand.Phone = in.Phone

		if err := deps.Candidates.Update(ctx, cand); err != nil {
			log.WithError(err).WithField("candidate_id", candidateID).
				Error("me/profile: profile update failed")
			httpmw.ProblemJSON(w, http.StatusInternalServerError,
				"update_failed", "could not save profile")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}
}

func profileToResponse(cand *domain.CandidateProfile) MeProfileResponse {
	return MeProfileResponse{
		ProfileID:          cand.ProfileID,
		Status:             string(cand.Status),
		Name:               cand.Name,
		CurrentTitle:       cand.CurrentTitle,
		Phone:              cand.Phone,
		PreferredCountries: cand.PreferredCountries,
		PreferredRegions:   cand.PreferredRegions,
		RemotePreference:   cand.RemotePreference,
		Languages:          cand.Languages,
		PlanID:             cand.PlanID,
		Subscription:       string(cand.Subscription),
	}
}
