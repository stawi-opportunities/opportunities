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

func echoCandidate(w http.ResponseWriter, r *http.Request) {
	id := httpmw.CandidateFromContext(r.Context())
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(id))
}

func requestWithClaims(method, path, subject string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	claims := &security.AuthenticationClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: subject},
	}
	return r.WithContext(claims.ClaimsToContext(r.Context()))
}

func TestCandidateAuth_PrefersClaimsSubject(t *testing.T) {
	t.Parallel()
	h := httpmw.CandidateAuth(http.HandlerFunc(echoCandidate))

	r := requestWithClaims(http.MethodGet, "/me/x", "claims-sub")
	r.Header.Set("X-Candidate-ID", "header-id")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "claims-sub", rec.Body.String(),
		"claims subject must take precedence over the X-Candidate-ID header")
}

func TestCandidateAuth_FallsBackToHeader(t *testing.T) {
	t.Parallel()
	h := httpmw.CandidateAuth(http.HandlerFunc(echoCandidate))

	r := httptest.NewRequest(http.MethodGet, "/me/x", nil)
	r.Header.Set("X-Candidate-ID", "header-id")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "header-id", rec.Body.String())
}

func TestCandidateAuth_NeitherIs401(t *testing.T) {
	t.Parallel()
	h := httpmw.CandidateAuth(http.HandlerFunc(echoCandidate))

	r := httptest.NewRequest(http.MethodGet, "/me/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Contains(t, rec.Body.String(), "unauthorized")
}

func TestNewCandidateAuth_NilAuthenticator_HeaderOnlyPath(t *testing.T) {
	t.Parallel()
	mw := httpmw.NewCandidateAuth(nil)
	h := mw(http.HandlerFunc(echoCandidate))

	r := httptest.NewRequest(http.MethodGet, "/me/x", nil)
	r.Header.Set("X-Candidate-ID", "cand_1")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "cand_1", rec.Body.String())
}
