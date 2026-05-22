package v1_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	v1 "github.com/stawi-opportunities/opportunities/apps/applications/service/http/v1"
	"github.com/stawi-opportunities/opportunities/pkg/applications"
)

func TestCandidateAuth_MissingHeaderReturns401(t *testing.T) {
	h := v1.CandidateAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	h := v1.CandidateAuth(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = v1.CandidateFromContext(r.Context())
	}))
	r := httptest.NewRequest("GET", "/anything", nil)
	r.Header.Set("X-Candidate-ID", "cand_42")
	h.ServeHTTP(httptest.NewRecorder(), r)
	require.Equal(t, "cand_42", got)
}

// Idempotency tests use a fake-store via direct DB — write the
// integration test in idempotency_test.go in this package OR test
// against the real store via testcontainers. For unit-level confidence,
// we mock the underlying *sql.DB driver below with a tiny in-memory
// shim. Keep this test focused on the wrapper logic.

// Use stubStore via a wrapper because the production IdempotencyStore
// expects *sql.DB. We delegate by embedding a sql.DB via a fake driver,
// but that's gnarly — instead, swap the test to use the real store
// against the migrations container in an integration test (which is
// already done in pkg/applications/idempotency_test.go). For unit
// scope, exercise the middleware indirectly by checking just the
// "key missing → pass through" path and the recorder behaviour.

func TestIdempotency_KeyMissing_PassesThrough(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("hello"))
	})
	mid := v1.Idempotency(v1.IdempotencyConfig{
		Store:      applications.NewIdempotencyStore(&sql.DB{}, time.Hour),
		RouteGroup: "applications.create",
	}, inner)
	wrapped := v1.CandidateAuth(mid)

	r := httptest.NewRequest("POST", "/anything", bytes.NewReader([]byte(`{}`)))
	r.Header.Set("X-Candidate-ID", "c")
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, r)
	require.Equal(t, http.StatusCreated, w.Code)
	require.Equal(t, "hello", w.Body.String())
}

func TestProblemJSON_Format(t *testing.T) {
	w := httptest.NewRecorder()
	v1.ProblemJSON(w, http.StatusBadRequest, "bad_input", "field missing")
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))
	var p v1.Problem
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p))
	require.Equal(t, "bad_input", p.Title)
	require.Equal(t, http.StatusBadRequest, p.Status)
	require.Equal(t, "field missing", p.Detail)
}

func TestProblemFromError_NotFound(t *testing.T) {
	w := httptest.NewRecorder()
	v1.ProblemFromError(w, applications.ErrNotFound)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestProblemFromError_InvalidTransition_IncludesAllowed(t *testing.T) {
	ie := &applications.InvalidTransitionError{From: applications.StatusNew, To: applications.StatusOffer,
		Allowed: []applications.Status{applications.StatusApplying, applications.StatusDismissed}}
	w := httptest.NewRecorder()
	v1.ProblemFromError(w, ie)
	require.Equal(t, http.StatusConflict, w.Code)

	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Equal(t, "invalid_transition", out["title"])
	require.Equal(t, string(applications.StatusNew), out["from"])
	require.Equal(t, string(applications.StatusOffer), out["to"])
	allowed, _ := out["allowed"].([]any)
	require.Len(t, allowed, 2)
}

// Silence "imported and not used" if the bytes import becomes unused
// after rewrites.
var _ = bytes.NewReader
var _ = context.Background
