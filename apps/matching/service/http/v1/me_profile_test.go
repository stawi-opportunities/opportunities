package v1_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	v1 "github.com/stawi-opportunities/opportunities/apps/matching/service/http/v1"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

type fakeProfileReaderWriter struct {
	candidate *domain.CandidateProfile
	getErr    error
	updateErr error
	calledID  string
}

func (f *fakeProfileReaderWriter) GetByID(_ context.Context, id string) (*domain.CandidateProfile, error) {
	f.calledID = id
	return f.candidate, f.getErr
}

func (f *fakeProfileReaderWriter) Update(_ context.Context, c *domain.CandidateProfile) error {
	f.candidate = c
	return f.updateErr
}

func TestProfileHandler_ReturnsCandidate(t *testing.T) {
	t.Parallel()
	cand := &domain.CandidateProfile{
		ProfileID:          "pro_abc",
		Status:             domain.CandidateActive,
		Name:               "Jane",
		CurrentTitle:       "Engineer",
		Phone:              "+123",
		PreferredCountries: "US,CA",
		PreferredRegions:   "remote",
		RemotePreference:   "yes",
		Languages:          "en,fr",
		PlanID:             "pro",
		Subscription:       domain.SubscriptionPaid,
	}
	fake := &fakeProfileReaderWriter{candidate: cand}
	h := httpmw.NewCandidateAuth(nil)(v1.ProfileHandler(v1.ProfileDeps{
		Candidates: fake,
	}))

	req := withProfileRequest(t, "GET", "/me", "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	candResp, ok := body["candidate"].(map[string]any)
	require.True(t, ok, "response must contain candidate object")
	require.Equal(t, "pro_abc", candResp["profile_id"])
	require.Equal(t, "active", candResp["status"])
	require.Equal(t, "Jane", candResp["name"])
	require.Equal(t, "Engineer", candResp["current_title"])
	require.Equal(t, "+123", candResp["phone"])
	require.Equal(t, "US,CA", candResp["preferred_countries"])
	require.Equal(t, "pro", candResp["plan_id"])
	require.Equal(t, "paid", candResp["subscription"])
}

func TestProfileHandler_NilCandidateReturnsNull(t *testing.T) {
	t.Parallel()
	fake := &fakeProfileReaderWriter{candidate: nil}
	h := httpmw.NewCandidateAuth(nil)(v1.ProfileHandler(v1.ProfileDeps{
		Candidates: fake,
	}))

	req := withProfileRequest(t, "GET", "/me", "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Nil(t, body["candidate"])
}

func TestProfileHandler_LookupErrorReturns502(t *testing.T) {
	t.Parallel()
	fake := &fakeProfileReaderWriter{getErr: errors.New("db down")}
	h := httpmw.NewCandidateAuth(nil)(v1.ProfileHandler(v1.ProfileDeps{
		Candidates: fake,
	}))

	req := withProfileRequest(t, "GET", "/me", "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Nil(t, body["candidate"])
}

func TestProfileUpdateHandler_UpdatesFields(t *testing.T) {
	t.Parallel()
	cand := &domain.CandidateProfile{
		Name:         "Old",
		CurrentTitle: "Junior",
		Phone:        "",
	}
	fake := &fakeProfileReaderWriter{candidate: cand}
	h := httpmw.NewCandidateAuth(nil)(v1.ProfileUpdateHandler(v1.ProfileDeps{
		Candidates: fake,
	}))

	payload := `{"name":"Jane","current_title":"Engineer","phone":"+123"}`
	req := withProfileRequest(t, "PUT", "/me/profile", payload)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, true, body["ok"])
	require.Equal(t, "Jane", fake.candidate.Name)
	require.Equal(t, "Engineer", fake.candidate.CurrentTitle)
	require.Equal(t, "+123", fake.candidate.Phone)
}

func TestProfileUpdateHandler_NilCandidateReturns404(t *testing.T) {
	t.Parallel()
	fake := &fakeProfileReaderWriter{candidate: nil}
	h := httpmw.NewCandidateAuth(nil)(v1.ProfileUpdateHandler(v1.ProfileDeps{
		Candidates: fake,
	}))

	payload := `{"name":"Jane","current_title":"Engineer","phone":"+123"}`
	req := withProfileRequest(t, "PUT", "/me/profile", payload)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestProfileUpdateHandler_InvalidJSONReturns400(t *testing.T) {
	t.Parallel()
	fake := &fakeProfileReaderWriter{candidate: &domain.CandidateProfile{}}
	h := httpmw.NewCandidateAuth(nil)(v1.ProfileUpdateHandler(v1.ProfileDeps{
		Candidates: fake,
	}))

	req := withProfileRequest(t, "PUT", "/me/profile", "not-json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// withProfileRequest builds a test request as if it passed through
// httpmw.CandidateAuth — the X-Candidate-ID header is set so the
// inner handler can read it from context after wrapping.
func withProfileRequest(t *testing.T, method, path, body string) *http.Request {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.Header.Set("X-Candidate-ID", "cand_test_1")
	return req
}
