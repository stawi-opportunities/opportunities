package httpmw_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/pitabwire/frame/v2/security"
	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

func TestRequireAdmin_SharedSecret(t *testing.T) {
	t.Parallel()
	called := false
	h := httpmw.RequireAdminFunc(httpmw.AdminAuthConfig{SharedSecret: "s3cret"}, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/_admin/x", nil)
	req.Header.Set("X-Admin-Token", "s3cret")
	rec := httptest.NewRecorder()
	h(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, called)
}

func TestRequireAdmin_WrongSecret(t *testing.T) {
	t.Parallel()
	h := httpmw.RequireAdminFunc(httpmw.AdminAuthConfig{SharedSecret: "s3cret"}, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not run")
	})
	req := httptest.NewRequest(http.MethodPost, "/_admin/x", nil)
	req.Header.Set("X-Admin-Token", "nope")
	rec := httptest.NewRecorder()
	h(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestRequireAdmin_NoConfig_FailClosed(t *testing.T) {
	t.Parallel()
	h := httpmw.RequireAdminFunc(httpmw.AdminAuthConfig{}, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not run")
	})
	req := httptest.NewRequest(http.MethodPost, "/_admin/x", nil)
	rec := httptest.NewRecorder()
	h(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireAdmin_AdminRole(t *testing.T) {
	t.Parallel()
	called := false
	h := httpmw.RequireAdminFunc(httpmw.AdminAuthConfig{}, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodPost, "/_admin/x", nil)
	claims := &security.AuthenticationClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "admin-user"},
	}
	claims.Roles = []string{"admin"}
	req = req.WithContext(claims.ClaimsToContext(req.Context()))
	rec := httptest.NewRecorder()
	h(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, called)
}
