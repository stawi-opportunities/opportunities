package v1

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

// ApplyDetails is the subscriber-only payload for application instructions.
// Public GET /api/jobs/{slug} never includes how_to_apply — only has_how_to_apply.
type ApplyDetails struct {
	OpportunityID string `json:"opportunity_id"`
	Slug          string `json:"slug"`
	ApplyURL      string `json:"apply_url"`
	HowToApply    string `json:"how_to_apply,omitempty"`
	// Locked is true when the candidate is authenticated but not on an
	// active/trial plan — the body is omitted and the UI shows a paywall.
	Locked bool `json:"locked,omitempty"`
}

// ApplyDetailsReader loads paywalled apply fields for one opportunity.
type ApplyDetailsReader interface {
	GetApplyDetails(ctx context.Context, idOrSlug string) (canonicalID, slug, applyURL, howToApply string, err error)
}

// ApplyDetailsDeps wires GET /me/opportunities/{id}/apply.
type ApplyDetailsDeps struct {
	Candidates CandidateProfileReader
	Store      ApplyDetailsReader
}

// ApplyDetailsHandler serves application instructions behind the subscription
// paywall.
//
//	GET /me/opportunities/{id}/apply
//	→ 200 { opportunity_id, slug, apply_url, how_to_apply } when active/trial
//	→ 200 { …, locked: true } without how_to_apply when free/cancelled
//	→ 404 when the opportunity is missing
func ApplyDetailsHandler(deps ApplyDetailsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			httpmw.ProblemJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
			return
		}
		ctx := r.Context()
		log := util.Log(ctx)
		candidateID := httpmw.CandidateFromContext(ctx)
		idOrSlug := strings.TrimSpace(r.PathValue("id"))
		if idOrSlug == "" {
			httpmw.ProblemJSON(w, http.StatusBadRequest, "id_required", "opportunity id or slug is required")
			return
		}
		if deps.Store == nil || deps.Candidates == nil {
			httpmw.ProblemJSON(w, http.StatusBadGateway, "apply_details_unavailable", "apply details store is not configured")
			return
		}

		cand, err := deps.Candidates.GetByID(ctx, candidateID)
		if err != nil {
			log.WithError(err).WithField("candidate_id", candidateID).
				Error("me/apply: candidate lookup failed")
			httpmw.ProblemJSON(w, http.StatusBadGateway, "candidate_lookup_failed", "could not load candidate profile")
			return
		}

		canonicalID, slug, applyURL, howToApply, err := deps.Store.GetApplyDetails(ctx, idOrSlug)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				httpmw.ProblemJSON(w, http.StatusNotFound, "not_found", "opportunity not found")
				return
			}
			log.WithError(err).WithField("id", idOrSlug).Error("me/apply: opportunity lookup failed")
			httpmw.ProblemJSON(w, http.StatusBadGateway, "opportunity_lookup_failed", "could not load opportunity")
			return
		}

		out := ApplyDetails{
			OpportunityID: canonicalID,
			Slug:          slug,
			ApplyURL:      applyURL,
		}
		if statusFromCandidate(cand) == "active" {
			out.HowToApply = howToApply
		} else {
			out.Locked = true
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// SQLApplyDetails implements ApplyDetailsReader against the shared
// opportunities table.
type SQLApplyDetails struct {
	DB *sql.DB
}

func (s *SQLApplyDetails) GetApplyDetails(ctx context.Context, idOrSlug string) (canonicalID, slug, applyURL, howToApply string, err error) {
	if s == nil || s.DB == nil {
		return "", "", "", "", errors.New("apply details: database not configured")
	}
	const q = `
SELECT canonical_id, slug, apply_url, COALESCE(how_to_apply, '')
  FROM opportunities
 WHERE (canonical_id = $1 OR slug = $1)
   AND hidden = false
   AND status = 'active'
 LIMIT 1`
	err = s.DB.QueryRowContext(ctx, q, idOrSlug).Scan(&canonicalID, &slug, &applyURL, &howToApply)
	return canonicalID, slug, applyURL, howToApply, err
}
