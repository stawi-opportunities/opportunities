package handlers

import (
	"net/http"

	"github.com/golang-jwt/jwt/v5"
	"github.com/pitabwire/frame/v2/security"
	securityhttp "github.com/pitabwire/frame/v2/security/interceptors/httptor"

	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

// TenancyAuth ensures JWT (or dev headers) produce claims with profile_id,
// tenant_id, and partition_id — aligned with identity/tenancy enrichment.
func TenancyAuth(authenticator security.Authenticator, allowHeaders bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			claims := security.ClaimsFromContext(ctx)
			if claims == nil && allowHeaders {
				pid := r.Header.Get("X-Profile-ID")
				tid := r.Header.Get("X-Tenant-ID")
				part := r.Header.Get("X-Partition-ID")
				if pid != "" && tid != "" && part != "" {
					c := &security.AuthenticationClaims{
						TenantID:    tid,
						PartitionID: part,
						ProfileID:   pid,
						RegisteredClaims: jwt.RegisteredClaims{
							Subject: pid,
						},
					}
					ctx = c.ClaimsToContext(ctx)
					claims = security.ClaimsFromContext(ctx)
				}
			}
			if claims == nil {
				httpmw.ProblemJSON(w, http.StatusUnauthorized, "unauthorized", "missing authentication")
				return
			}
			if claims.GetTenantID() == "" || claims.GetPartitionID() == "" {
				httpmw.ProblemJSON(w, http.StatusForbidden, "forbidden", "tenant_id and partition_id required")
				return
			}
			pid := claims.GetProfileID()
			if pid == "" {
				pid = claims.Subject
			}
			if pid == "" {
				httpmw.ProblemJSON(w, http.StatusUnauthorized, "unauthorized", "profile_id required")
				return
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
		if authenticator == nil {
			return inner
		}
		return securityhttp.AuthenticationMiddleware(inner, authenticator)
	}
}
