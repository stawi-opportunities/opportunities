// Package httpmw provides shared HTTP middleware used by every
// /api/me/* surface in the platform.
package httpmw

import (
	"context"
	"net/http"
)

type candidateKey struct{}

// CandidateAuth pulls the candidate identity from the X-Candidate-ID
// header. Missing or empty → 401 problem+json. OIDC bearer validation
// is layered on top later.
func CandidateAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Candidate-ID")
		if id == "" {
			ProblemJSON(w, http.StatusUnauthorized,
				"unauthorized", "X-Candidate-ID header required")
			return
		}
		ctx := context.WithValue(r.Context(), candidateKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// CandidateFromContext returns the authenticated candidate ID. Panics
// if called from a route that wasn't wrapped in CandidateAuth.
func CandidateFromContext(ctx context.Context) string {
	v, _ := ctx.Value(candidateKey{}).(string)
	if v == "" {
		panic("httpmw: CandidateFromContext called outside CandidateAuth")
	}
	return v
}
