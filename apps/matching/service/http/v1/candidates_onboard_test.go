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
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

type fakeOnboardStore struct {
	txErr        error
	updatedCand  *domain.CandidateProfile
	draftCleared bool
}

func (f *fakeOnboardStore) OnboardAtomically(_ context.Context, candidateID string, mutate func(*domain.CandidateProfile)) error {
	if f.txErr != nil {
		return f.txErr
	}
	c := &domain.CandidateProfile{ProfileID: candidateID}
	c.ID = candidateID
	mutate(c)
	f.updatedCand = c
	f.draftCleared = true
	return nil
}

func reqOnboard(t *testing.T, body string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/candidates/onboard", bytes.NewBufferString(body))
	r.Header.Set("X-Candidate-ID", "cand_onboard_1")
	r.Header.Set("Content-Type", "application/json")
	return r
}

const validBody = `{
  "target_job_title": "Backend Engineer",
  "experience_level": "mid",
  "job_search_status": "actively_looking",
  "wants_ats_report": true,
  "preferred_regions": ["Africa"],
  "preferred_timezones": ["UTC+0"],
  "preferred_languages": ["English"],
  "job_types": ["Full-time"],
  "country": "KE",
  "plan": "starter",
  "agree_terms": true
}`

func TestCandidatesOnboardHandler_Success(t *testing.T) {
	t.Parallel()
	store := &fakeOnboardStore{}
	h := httpmw.CandidateAuth(v1.CandidatesOnboardHandler(v1.CandidatesOnboardDeps{Store: store}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, reqOnboard(t, validBody))

	require.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "cand_onboard_1", resp["id"])
	require.Equal(t, "cand_onboard_1", resp["profile_id"])
	require.NotNil(t, store.updatedCand)
	require.Equal(t, "Backend Engineer", store.updatedCand.TargetJobTitle)
	require.Equal(t, "mid", store.updatedCand.ExperienceLevel)
	require.Equal(t, "starter", store.updatedCand.PlanID)
	require.Equal(t, domain.CandidateActive, store.updatedCand.Status)
	require.True(t, store.draftCleared)
}

func TestCandidatesOnboardHandler_RejectsMissingRequiredFields(t *testing.T) {
	t.Parallel()
	store := &fakeOnboardStore{}
	h := httpmw.CandidateAuth(v1.CandidatesOnboardHandler(v1.CandidatesOnboardDeps{Store: store}))

	tests := map[string]string{
		"missing target_job_title": `{"experience_level":"mid","job_search_status":"a","country":"KE","plan":"starter","agree_terms":true,"wants_ats_report":false,"preferred_regions":[],"preferred_timezones":[],"preferred_languages":[],"job_types":[]}`,
		"missing plan":             `{"target_job_title":"PM","experience_level":"mid","job_search_status":"a","country":"KE","agree_terms":true,"wants_ats_report":false,"preferred_regions":[],"preferred_timezones":[],"preferred_languages":[],"job_types":[]}`,
		"agree_terms false":        `{"target_job_title":"PM","experience_level":"mid","job_search_status":"a","country":"KE","plan":"starter","agree_terms":false,"wants_ats_report":false,"preferred_regions":[],"preferred_timezones":[],"preferred_languages":[],"job_types":[]}`,
		"invalid plan":             `{"target_job_title":"PM","experience_level":"mid","job_search_status":"a","country":"KE","plan":"gold","agree_terms":true,"wants_ats_report":false,"preferred_regions":[],"preferred_timezones":[],"preferred_languages":[],"job_types":[]}`,
		"unparseable":              `not json`,
	}
	for name, body := range tests {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, reqOnboard(t, body))
		require.Equal(t, http.StatusBadRequest, rec.Code, name)
	}
	require.Nil(t, store.updatedCand, "no transaction should run for bad payloads")
}

func TestCandidatesOnboardHandler_StoreErrorReturnsProblemJSON(t *testing.T) {
	t.Parallel()
	store := &fakeOnboardStore{txErr: errors.New("db wedged")}
	h := httpmw.CandidateAuth(v1.CandidatesOnboardHandler(v1.CandidatesOnboardDeps{Store: store}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, reqOnboard(t, validBody))

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
	require.Contains(t, rec.Body.String(), "onboard_failed")
}

func TestCandidatesOnboardHandler_MethodOther(t *testing.T) {
	t.Parallel()
	store := &fakeOnboardStore{}
	h := httpmw.CandidateAuth(v1.CandidatesOnboardHandler(v1.CandidatesOnboardDeps{Store: store}))

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/candidates/onboard", nil)
	r.Header.Set("X-Candidate-ID", "cand_x")
	h.ServeHTTP(rec, r)
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}
