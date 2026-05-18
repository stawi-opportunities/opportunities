package authz_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/authz"
)

func fakeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	body, _ := json.Marshal(claims)
	bodyB64 := base64.RawURLEncoding.EncodeToString(body)
	return header + "." + bodyB64 + ".sig"
}

func TestProfileIDFromJWT_Happy(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+fakeJWT(t, map[string]any{"sub": "user-42"}))
	if got := authz.ProfileIDFromJWT(req); got != "user-42" {
		t.Fatalf("got %q, want user-42", got)
	}
}

func TestProfileIDFromJWT_NoHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := authz.ProfileIDFromJWT(req); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestProfileIDFromJWT_WrongScheme(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Basic abc")
	if got := authz.ProfileIDFromJWT(req); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestProfileIDFromJWT_NotThreeParts(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer one.two")
	if got := authz.ProfileIDFromJWT(req); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestProfileIDFromJWT_BadBase64(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer abc.!!!.sig")
	if got := authz.ProfileIDFromJWT(req); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestProfileIDFromJWT_MissingSub(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+fakeJWT(t, map[string]any{"name": "X"}))
	if got := authz.ProfileIDFromJWT(req); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestProfileIDFromJWT_NilRequest(t *testing.T) {
	if got := authz.ProfileIDFromJWT(nil); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}
