package httpmw_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

func TestProblemJSON_Format(t *testing.T) {
	w := httptest.NewRecorder()
	httpmw.ProblemJSON(w, http.StatusBadRequest, "bad_input", "field missing")
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))
	var p httpmw.Problem
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p))
	require.Equal(t, "bad_input", p.Title)
	require.Equal(t, http.StatusBadRequest, p.Status)
	require.Equal(t, "field missing", p.Detail)
	require.Equal(t, "about:blank", p.Type)
}

func TestProblemJSONExtra_MergesFields(t *testing.T) {
	w := httptest.NewRecorder()
	httpmw.ProblemJSONExtra(w, http.StatusConflict, "conflict", "some detail",
		map[string]any{"allowed": []string{"a", "b"}})
	require.Equal(t, http.StatusConflict, w.Code)
	require.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Equal(t, "conflict", out["title"])
	require.Equal(t, float64(http.StatusConflict), out["status"])
	allowed, _ := out["allowed"].([]any)
	require.Len(t, allowed, 2)
}

func TestCandidateAuth_MissingHeaderReturns401(t *testing.T) {
	h := httpmw.NewCandidateAuth(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("inner handler should not run")
	}))
	r := httptest.NewRequest("GET", "/anything", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))
}

func TestCandidateAuth_PassesIDToContext(t *testing.T) {
	var got string
	h := httpmw.NewCandidateAuth(nil)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = httpmw.CandidateFromContext(r.Context())
	}))
	r := httptest.NewRequest("GET", "/anything", nil)
	r.Header.Set("X-Candidate-ID", "cand_42")
	h.ServeHTTP(httptest.NewRecorder(), r)
	require.Equal(t, "cand_42", got)
}

func TestIdempotency_KeyMissing_PassesThrough(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("hello"))
	})
	mid := httpmw.Idempotency(httpmw.IdempotencyConfig{
		Store:      nil, // no store needed when key is missing
		RouteGroup: "test",
	}, inner)
	wrapped := httpmw.NewCandidateAuth(nil)(mid)

	r := httptest.NewRequest("POST", "/anything", nil)
	r.Header.Set("X-Candidate-ID", "c")
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, r)
	require.Equal(t, http.StatusCreated, w.Code)
	require.Equal(t, "hello", w.Body.String())
}
