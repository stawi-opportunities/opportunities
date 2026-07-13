package v1

import (
	"context"
	"errors"
	"net/http"

	"github.com/stawi-opportunities/opportunities/pkg/applications"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

// CandidateAuth pulls the candidate identity from a verified JWT subject,
// or (in tests / local without OIDC) from the X-Candidate-ID header via
// NewCandidateAuth(nil).
func CandidateAuth(next http.Handler) http.Handler {
	return httpmw.NewCandidateAuth(nil)(next)
}

// CandidateFromContext returns the authenticated candidate ID from the context.
// Delegates to pkg/httpmw.CandidateFromContext.
func CandidateFromContext(ctx context.Context) string { return httpmw.CandidateFromContext(ctx) }

// Problem is the RFC 9457 problem+json envelope (re-exported from pkg/httpmw).
type Problem = httpmw.Problem

// ProblemJSON writes an application/problem+json error response.
func ProblemJSON(w http.ResponseWriter, status int, title, detail string) {
	httpmw.ProblemJSON(w, status, title, detail)
}

// IdempotencyConfig configures the idempotency middleware.
type IdempotencyConfig = httpmw.IdempotencyConfig

// Idempotency wraps a mutating handler with idempotency replay protection.
func Idempotency(cfg IdempotencyConfig, next http.Handler) http.Handler {
	return httpmw.Idempotency(cfg, next)
}

// ProblemFromError maps known sentinel errors to standard problems.
// Unknown errors return 500 with a generic title.
func ProblemFromError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, applications.ErrNotFound):
		httpmw.ProblemJSON(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, applications.ErrAlreadyExists):
		httpmw.ProblemJSON(w, http.StatusConflict, "already_exists", err.Error())
	default:
		var ie *applications.InvalidTransitionError
		if errors.As(err, &ie) {
			allowed := make([]string, 0, len(ie.Allowed))
			for _, s := range ie.Allowed {
				allowed = append(allowed, string(s))
			}
			httpmw.ProblemJSONExtra(w, http.StatusConflict, "invalid_transition", err.Error(),
				map[string]any{
					"from":    string(ie.From),
					"to":      string(ie.To),
					"allowed": allowed,
				})
			return
		}
		httpmw.ProblemJSON(w, http.StatusInternalServerError, "internal_error", err.Error())
	}
}
