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

type authModeKey struct{}

// AuthMode controls whether CandidateAuth accepts the X-Candidate-ID
// header as a fallback when OIDC claims are absent.
type AuthMode int

const (
	// AuthModeStrict requires a verified JWT subject. Header spoofing
	// is rejected. This is the production default when an authenticator
	// is configured.
	AuthModeStrict AuthMode = iota
	// AuthModeAllowHeader permits X-Candidate-ID when claims are absent.
	// Used by unit tests and local dev without OIDC.
	AuthModeAllowHeader
)

// WithAuthMode stores the auth mode on the request context.
func WithAuthMode(ctx context.Context, mode AuthMode) context.Context {
	return context.WithValue(ctx, authModeKey{}, mode)
}

func authModeFromContext(ctx context.Context) AuthMode {
	if v, ok := ctx.Value(authModeKey{}).(AuthMode); ok {
		return v
	}
	// Default strict: never silently trust a client-supplied identity.
	return AuthModeStrict
}

// CandidateAuth pulls the candidate identity from the OIDC subject
// claim when Frame's AuthenticationMiddleware has run upstream.
//
// When AuthModeAllowHeader is on the context (or the request was
// wrapped with NewCandidateAuth(nil) / NewCandidateAuthAllowHeader),
// it also accepts X-Candidate-ID for tests and local dev. Production
// paths MUST use NewCandidateAuth(authenticator) so JWT verification
// runs and header spoofing is impossible.
//
// Missing identity → 401 problem+json.
func CandidateAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		id := ""
		if claims := security.ClaimsFromContext(ctx); claims != nil {
			id = claims.Subject
		}
		if id == "" && authModeFromContext(ctx) == AuthModeAllowHeader {
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

// NewCandidateAuth returns the authentication chain for private candidate
// routes: outer JWT verification (Frame AuthenticationMiddleware) then
// CandidateAuth subject extraction.
//
// Default / production (authenticator non-nil): JWT only — X-Candidate-ID
// is ignored. This is the secure default; wrap every private handler with
// this. Public endpoints must be registered without this middleware.
//
// Dev/tests (authenticator nil): header fallback is enabled. Process boot
// should refuse nil authenticator unless AUTH_REQUIRE_JWT=false so prod
// never falls open.
func NewCandidateAuth(authenticator security.Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		// Strict JWT when verifier present; header only when tests pass nil.
		mode := AuthModeStrict
		if authenticator == nil {
			mode = AuthModeAllowHeader
		}
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := WithAuthMode(r.Context(), mode)
			CandidateAuth(next).ServeHTTP(w, r.WithContext(ctx))
		})
		if authenticator == nil {
			return inner
		}
		return securityhttp.AuthenticationMiddleware(inner, authenticator)
	}
}

// NewCandidateAuthAllowHeader is an explicit header-auth wrapper for
// tests. Prefer NewCandidateAuth(nil) for the same behaviour.
func NewCandidateAuthAllowHeader() func(http.Handler) http.Handler {
	return NewCandidateAuth(nil)
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

// CandidateFromContextOptional returns the candidate ID when CandidateAuth
// has run, or ("", false) otherwise. Safe to call from dual-path handlers.
func CandidateFromContextOptional(ctx context.Context) (string, bool) {
	v, _ := ctx.Value(candidateKey{}).(string)
	if v == "" {
		return "", false
	}
	return v, true
}
