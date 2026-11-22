package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pitabwire/frame/v2/security"

	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

type stubAuthenticator struct{ subject string }

func (s stubAuthenticator) Authenticate(ctx context.Context, _ string, _ ...security.AuthOption) (context.Context, error) {
	if s.subject == "" {
		return ctx, errors.New("invalid token")
	}
	c := &security.AuthenticationClaims{}
	c.Subject = s.subject
	return c.ClaimsToContext(ctx), nil
}

// An unsigned token whose payload is {"sub":"attacker"}.
const forgedToken = "Bearer eyJhbGciOiJub25lIn0.eyJzdWIiOiJhdHRhY2tlciJ9.x"

func TestUserFlagRoute_Auth(t *testing.T) {
	cases := []struct {
		name      string
		auth      security.Authenticator
		wantCode  int
		wantInner string // profile id the inner handler should see; "" = must not run
	}{
		{name: "no authenticator fails closed", auth: nil, wantCode: http.StatusServiceUnavailable},
		// Frame's middleware answers an unverifiable token with 403.
		{name: "forged token rejected", auth: stubAuthenticator{}, wantCode: http.StatusForbidden},
		{name: "verified token reaches handler", auth: stubAuthenticator{subject: "profile-1"}, wantCode: http.StatusNoContent, wantInner: "profile-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = httpmw.ProfileIDFromContext(r.Context())
				w.WriteHeader(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodPost, "/opportunities/some-job/flag", nil)
			req.Header.Set("Authorization", forgedToken)
			rec := httptest.NewRecorder()

			userFlagRoute(tc.auth, inner).ServeHTTP(rec, req)

			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if got != tc.wantInner {
				t.Fatalf("inner saw profile %q, want %q", got, tc.wantInner)
			}
		})
	}
}
