package v1_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	v1 "github.com/stawi-opportunities/opportunities/apps/matching/service/http/v1"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

type fakeDraftStore struct {
	getResp  []byte
	getErr   error
	setBody  []byte
	setErr   error
	setCalls int
}

func (f *fakeDraftStore) GetOnboardingDraft(_ context.Context, _ string) ([]byte, error) {
	return f.getResp, f.getErr
}

func (f *fakeDraftStore) SetOnboardingDraft(_ context.Context, _ string, draft []byte) error {
	f.setBody = draft
	f.setCalls++
	return f.setErr
}

func reqWithCandidate(t *testing.T, method, path, body string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	r.Header.Set("X-Candidate-ID", "cand_h_1")
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	return r
}

func TestOnboardingHandler_GetReturnsDraft(t *testing.T) {
	t.Parallel()
	draft := []byte(`{"step":2,"fields":{"target_job_title":"PM"},"updated_at":"2026-05-23T10:00:00Z"}`)
	store := &fakeDraftStore{getResp: draft}
	h := httpmw.CandidateAuth(v1.OnboardingHandler(v1.OnboardingDeps{Drafts: store}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, reqWithCandidate(t, http.MethodGet, "/me/onboarding", ""))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.JSONEq(t, string(draft), rec.Body.String())
}

func TestOnboardingHandler_GetEmptyDraftRendersDefault(t *testing.T) {
	t.Parallel()
	store := &fakeDraftStore{getResp: []byte(`{}`)}
	h := httpmw.CandidateAuth(v1.OnboardingHandler(v1.OnboardingDeps{Drafts: store}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, reqWithCandidate(t, http.MethodGet, "/me/onboarding", ""))

	require.Equal(t, http.StatusOK, rec.Code)
	// Empty draft renders as the canonical wizard start: step 1,
	// no fields. The wizard relies on this default to skip a
	// post-load conditional.
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, float64(1), resp["step"])
	require.Equal(t, map[string]any{}, resp["fields"])
}

func TestOnboardingHandler_GetStoreErrorReturnsProblemJSON(t *testing.T) {
	t.Parallel()
	store := &fakeDraftStore{getErr: errors.New("db down")}
	h := httpmw.CandidateAuth(v1.OnboardingHandler(v1.OnboardingDeps{Drafts: store}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, reqWithCandidate(t, http.MethodGet, "/me/onboarding", ""))

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
	require.Contains(t, rec.Body.String(), "draft_lookup_failed")
}

func TestOnboardingHandler_PutPersistsDraft(t *testing.T) {
	t.Parallel()
	store := &fakeDraftStore{}
	h := httpmw.CandidateAuth(v1.OnboardingHandler(v1.OnboardingDeps{Drafts: store}))

	body := `{"step":2,"fields":{"target_job_title":"PM","experience_level":"mid"}}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, reqWithCandidate(t, http.MethodPut, "/me/onboarding", body))

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, 1, store.setCalls)
	// Handler adds updated_at server-side so the wizard doesn't have
	// to (clock skew between client + server would otherwise produce
	// confusing "draft is from the future" displays).
	var persisted map[string]any
	require.NoError(t, json.Unmarshal(store.setBody, &persisted))
	require.Equal(t, float64(2), persisted["step"])
	require.NotEmpty(t, persisted["updated_at"])
}

func TestOnboardingHandler_PutRejectsInvalidStep(t *testing.T) {
	t.Parallel()
	store := &fakeDraftStore{}
	h := httpmw.CandidateAuth(v1.OnboardingHandler(v1.OnboardingDeps{Drafts: store}))

	for _, badBody := range []string{
		`{"step":0,"fields":{}}`,     // below range
		`{"step":4,"fields":{}}`,     // above range
		`{"step":"two","fields":{}}`, // wrong type
		`{}`,                         // missing step
		`not json`,                   // unparseable
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, reqWithCandidate(t, http.MethodPut, "/me/onboarding", badBody))
		require.Equal(t, http.StatusBadRequest, rec.Code, "body=%q", badBody)
	}
	require.Zero(t, store.setCalls, "no Set call should land for bad bodies")
}

func TestOnboardingHandler_PutPersistFailureReturnsProblemJSON(t *testing.T) {
	t.Parallel()
	store := &fakeDraftStore{setErr: errors.New("disk full")}
	h := httpmw.CandidateAuth(v1.OnboardingHandler(v1.OnboardingDeps{Drafts: store}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, reqWithCandidate(t, http.MethodPut, "/me/onboarding",
		`{"step":1,"fields":{"target_job_title":"PM"}}`))

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, rec.Body.String(), "draft_persist_failed")
}

func TestOnboardingHandler_MethodOther(t *testing.T) {
	t.Parallel()
	store := &fakeDraftStore{}
	h := httpmw.CandidateAuth(v1.OnboardingHandler(v1.OnboardingDeps{Drafts: store}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, reqWithCandidate(t, http.MethodPost, "/me/onboarding", `{}`))
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}
