package v1_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	v1 "github.com/stawi-opportunities/opportunities/apps/matching/service/http/v1"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

type fakeStarter struct {
	appID      string
	appliedAt  time.Time
	err        error
	calledWith [3]string // candidate, opp, method
}

func (f *fakeStarter) StartApplication(_ context.Context, cand, opp, method string) (string, time.Time, error) {
	f.calledWith = [3]string{cand, opp, method}
	return f.appID, f.appliedAt, f.err
}

func TestApplicationsHandler_PostApplies(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Second)
	s := &fakeStarter{appID: "app_xyz", appliedAt: now}
	h := httpmw.CandidateAuth(v1.ApplicationsHandler(v1.ApplicationsDeps{Starter: s}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/me/applications", bytes.NewBufferString(`{"opportunity_id":"opp_42","method":"manual"}`))
	req.Header.Set("X-Candidate-ID", "cand_a")
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "app_xyz", resp["application_id"])
	require.Equal(t, "applied", resp["status"])
	require.Equal(t, [3]string{"cand_a", "opp_42", "manual"}, s.calledWith)
}

func TestApplicationsHandler_DefaultsMethodToManual(t *testing.T) {
	t.Parallel()
	s := &fakeStarter{appID: "app_y", appliedAt: time.Now()}
	h := httpmw.CandidateAuth(v1.ApplicationsHandler(v1.ApplicationsDeps{Starter: s}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/me/applications", bytes.NewBufferString(`{"opportunity_id":"opp_42"}`))
	req.Header.Set("X-Candidate-ID", "cand_a")
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, "manual", s.calledWith[2])
}

func TestApplicationsHandler_RejectsMissingID(t *testing.T) {
	t.Parallel()
	s := &fakeStarter{}
	h := httpmw.CandidateAuth(v1.ApplicationsHandler(v1.ApplicationsDeps{Starter: s}))

	for _, body := range []string{`{}`, `{"opportunity_id":""}`, `not json`} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/me/applications", bytes.NewBufferString(body))
		req.Header.Set("X-Candidate-ID", "cand_a")
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code, "body=%q", body)
	}
	require.Equal(t, [3]string{"", "", ""}, s.calledWith)
}

func TestApplicationsHandler_StarterErrorIs502(t *testing.T) {
	t.Parallel()
	s := &fakeStarter{err: errors.New("db wedged")}
	h := httpmw.CandidateAuth(v1.ApplicationsHandler(v1.ApplicationsDeps{Starter: s}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/me/applications", bytes.NewBufferString(`{"opportunity_id":"opp_42"}`))
	req.Header.Set("X-Candidate-ID", "cand_a")
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadGateway, rec.Code)
}

func TestApplicationsHandler_MethodOther(t *testing.T) {
	t.Parallel()
	s := &fakeStarter{}
	h := httpmw.CandidateAuth(v1.ApplicationsHandler(v1.ApplicationsDeps{Starter: s}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/me/applications", nil)
	req.Header.Set("X-Candidate-ID", "cand_a")
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}
