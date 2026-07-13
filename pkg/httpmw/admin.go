package httpmw

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/pitabwire/frame/v2/security"
	securityhttp "github.com/pitabwire/frame/v2/security/interceptors/httptor"
)

// AdminAuthConfig configures RequireAdmin.
type AdminAuthConfig struct {
	// Authenticator verifies JWTs when non-nil. Claims must include the
	// "admin" role.
	Authenticator security.Authenticator
	// SharedSecret accepts X-Admin-Token / Authorization: Bearer <secret>
	// for machine callers (Trustage cron). Empty disables secret auth.
	SharedSecret string
	// AllowUnauthenticated is for unit tests only. Never set in production.
	AllowUnauthenticated bool
}

// RequireAdmin gates a handler on either:
//  1. a shared secret in X-Admin-Token / Authorization Bearer (Trustage), or
//  2. a verified JWT with the "admin" role.
//
// Shared-secret checks run BEFORE JWT middleware so Trustage callers that
// only present X-Admin-Token are not rejected by the JWT verifier.
//
// If neither authenticator nor shared secret is configured and
// AllowUnauthenticated is false, every request is rejected with 401 so
// misconfigured deploys fail closed rather than open.
func RequireAdmin(cfg AdminAuthConfig, next http.Handler) http.Handler {
	core := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Shared secret path (Trustage / internal automation) — no JWT required.
		if cfg.SharedSecret != "" {
			token := r.Header.Get("X-Admin-Token")
			if token == "" {
				if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
					token = strings.TrimPrefix(auth, "Bearer ")
				}
			}
			if token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(cfg.SharedSecret)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
			// If the request only had a secret attempt (no Bearer JWT shape),
			// fail here rather than falling through to JWT.
			if r.Header.Get("X-Admin-Token") != "" {
				ProblemJSON(w, http.StatusForbidden, "forbidden", "invalid admin token")
				return
			}
		}

		// JWT admin role path — claims must already be on context (see wrapper).
		claims := security.ClaimsFromContext(r.Context())
		if claims != nil && containsRole(claims.GetRoles(), "admin") {
			next.ServeHTTP(w, r)
			return
		}

		if cfg.AllowUnauthenticated {
			next.ServeHTTP(w, r)
			return
		}

		if claims == nil {
			ProblemJSON(w, http.StatusUnauthorized, "unauthorized", "admin authentication required")
			return
		}
		ProblemJSON(w, http.StatusForbidden, "forbidden", "admin role or valid admin token required")
	})

	// Outer handler: accept shared secret without JWT; otherwise optionally
	// run JWT verification so role checks see claims.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cfg.SharedSecret != "" {
			token := r.Header.Get("X-Admin-Token")
			if token == "" {
				if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
					// Could be JWT or shared secret — try secret first.
					token = strings.TrimPrefix(auth, "Bearer ")
				}
			}
			if token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(cfg.SharedSecret)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
		}

		if cfg.Authenticator != nil && strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			// Only invoke JWT middleware when a Bearer token is present and
			// it is not the shared secret (already handled above).
			securityhttp.AuthenticationMiddleware(core, cfg.Authenticator).ServeHTTP(w, r)
			return
		}

		core.ServeHTTP(w, r)
	})
}

// RequireAdminFunc is RequireAdmin for http.HandlerFunc registrations.
func RequireAdminFunc(cfg AdminAuthConfig, next http.HandlerFunc) http.HandlerFunc {
	wrapped := RequireAdmin(cfg, next)
	return func(w http.ResponseWriter, r *http.Request) {
		wrapped.ServeHTTP(w, r)
	}
}

func containsRole(roles []string, want string) bool {
	for _, r := range roles {
		if r == want {
			return true
		}
	}
	return false
}
