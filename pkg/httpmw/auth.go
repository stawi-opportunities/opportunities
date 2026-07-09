// Package httpmw provides shared HTTP middleware used by every
// /api/me/* surface in the platform.
package httpmw

import (
	"context"
	"net/http"

	"github.com/pitabwire/frame/v2/security"
	securityhttp "github.com/pitabwire/frame/v2/security/interceptors/httptor"
)

type candidateKey struct{}

// CandidateAuth pulls the candidate identity from the OIDC subject
// claim when Frame's AuthenticationMiddleware has run upstream, and
// falls back to the X-Candidate-ID header for internal / test callers.
// The OIDC subject IS the candidate ID — it's the same identity Hydra
// issues to the profile service. Missing both → 401 problem+json.
//
// This is the inner middleware; it does NOT validate JWTs itself.
// Production callers wrap it with NewCandidateAuth(authenticator) so
// the Bearer token is verified before claims are trusted.
func CandidateAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		id := ""
		if claims := security.ClaimsFromContext(ctx); claims != nil {
			id = claims.Subject
		}
		if id == "" {
			id = r.Header.Get("X-Candidate-ID")
		}
		if id == "" {
			ProblemJSON(w, http.StatusUnauthorized,
				"unauthorized", "missing authentication")
			return
		}
		ctx = context.WithValue(ctx, candidateKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// NewCandidateAuth returns the full authentication chain a public
// /me/* route needs: outer JWT verification via Frame's
// securityhttp.AuthenticationMiddleware, inner subject-extraction via
// CandidateAuth. When authenticator is nil (unit tests, or a service
// started without OIDC env vars), only the inner middleware runs and
// CandidateAuth falls back to the X-Candidate-ID header — so existing
// tests that set the header continue to work unchanged.
func NewCandidateAuth(authenticator security.Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		inner := CandidateAuth(next)
		if authenticator == nil {
			return inner
		}
		return securityhttp.AuthenticationMiddleware(inner, authenticator)
	}
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
