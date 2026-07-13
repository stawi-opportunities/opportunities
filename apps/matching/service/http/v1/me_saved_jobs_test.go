package v1_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	v1 "github.com/stawi-opportunities/opportunities/apps/matching/service/http/v1"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

type fakeSaver struct {
	starErr      error
	unstarErr    error
	calledStar   [2]string
	calledUnstar [2]string
	starCalls    int
	unstarCalls  int
}

func (f *fakeSaver) Star(_ context.Context, c, o string) error {
	f.calledStar = [2]string{c, o}
	f.starCalls++
	return f.starErr
}
func (f *fakeSaver) Unstar(_ context.Context, c, o string) error {
	f.calledUnstar = [2]string{c, o}
	f.unstarCalls++
	return f.unstarErr
}

func TestSavedJobsHandler_PostStars(t *testing.T) {
	t.Parallel()
	s := &fakeSaver{}
	h := httpmw.NewCandidateAuth(nil)(v1.SavedJobsHandler(v1.SavedJobsDeps{Store: s}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/me/saved-jobs", bytes.NewBufferString(`{"opportunity_id":"opp_77"}`))
	req.Header.Set("X-Candidate-ID", "cand_x")
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, [2]string{"cand_x", "opp_77"}, s.calledStar)
}

func TestSavedJobsHandler_PostRejectsMissingID(t *testing.T) {
	t.Parallel()
	s := &fakeSaver{}
	h := httpmw.NewCandidateAuth(nil)(v1.SavedJobsHandler(v1.SavedJobsDeps{Store: s}))

	for _, body := range []string{`{}`, `{"opportunity_id":""}`, `not json`} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/me/saved-jobs", bytes.NewBufferString(body))
		req.Header.Set("X-Candidate-ID", "cand_x")
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code, "body=%q", body)
	}
	require.Zero(t, s.starCalls)
}

func TestSavedJobsHandler_DeleteUnstars(t *testing.T) {
	t.Parallel()
	s := &fakeSaver{}
	h := httpmw.NewCandidateAuth(nil)(v1.SavedJobsHandler(v1.SavedJobsDeps{Store: s}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/me/saved-jobs/opp_77", nil)
	req.Header.Set("X-Candidate-ID", "cand_x")
	// SetPathValue is how Go 1.22+ http.ServeMux populates {param}
	// outside of the mux's own dispatch path (which a test bypasses).
	req.SetPathValue("opportunity_id", "opp_77")
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, [2]string{"cand_x", "opp_77"}, s.calledUnstar)
}

func TestSavedJobsHandler_StarPersistErrorIs502(t *testing.T) {
	t.Parallel()
	s := &fakeSaver{starErr: errors.New("db wedged")}
	h := httpmw.NewCandidateAuth(nil)(v1.SavedJobsHandler(v1.SavedJobsDeps{Store: s}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/me/saved-jobs", bytes.NewBufferString(`{"opportunity_id":"opp_77"}`))
	req.Header.Set("X-Candidate-ID", "cand_x")
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadGateway, rec.Code)
}
