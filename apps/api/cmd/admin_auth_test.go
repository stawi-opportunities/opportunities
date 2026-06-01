package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pitabwire/frame/security"
)

func TestRequireAdmin_NoBearer_Returns401(t *testing.T) {
	called := false
	h := requireAdmin(func(w http.ResponseWriter, r *http.Request) { called = true })
	req := httptest.NewRequest("GET", "/admin/test", nil)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", rec.Code)
	}
	if called {
		t.Fatal("handler invoked despite missing token")
	}
}

func TestRequireAdmin_BearerButNoAdminRole_Returns403(t *testing.T) {
	called := false
	h := requireAdmin(func(w http.ResponseWriter, r *http.Request) { called = true })
	req := httptest.NewRequest("GET", "/admin/test", nil)
	req.Header.Set("Authorization", "Bearer fake-token")
	claims := &security.AuthenticationClaims{}
	claims.Roles = []string{"candidate"} // any non-admin role
	ctx := claims.ClaimsToContext(req.Context())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d; want 403", rec.Code)
	}
	if called {
		t.Fatal("handler invoked despite missing admin role")
	}
}

func TestRequireAdmin_BearerAndAdminRole_PassesThrough(t *testing.T) {
	called := false
	h := requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest("GET", "/admin/test", nil)
	req.Header.Set("Authorization", "Bearer fake-token")
	claims := &security.AuthenticationClaims{}
	claims.Roles = []string{"admin", "candidate"}
	ctx := claims.ClaimsToContext(req.Context())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	if !called {
		t.Fatal("handler not invoked despite admin role")
	}
}
