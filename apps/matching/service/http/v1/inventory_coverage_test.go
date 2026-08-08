package v1_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	v1 "github.com/stawi-opportunities/opportunities/apps/matching/service/http/v1"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
	"github.com/stawi-opportunities/opportunities/pkg/placement"
)

// --- GET /me/cv ---

type fakeCVProfiles struct {
	ref placement.ProfileCV
	err error
}

func (f *fakeCVProfiles) GetCVFileRef(_ context.Context, _ string) (placement.ProfileCV, error) {
	return f.ref, f.err
}

func (f *fakeCVProfiles) SetCVFileRef(context.Context, string, placement.ProfileCV) error {
	return nil
}

type fakePlacementStore struct {
	doc *placement.Document
	err error
}

func (f *fakePlacementStore) Get(_ context.Context, _ string) (*placement.Document, error) {
	return f.doc, f.err
}

func (f *fakePlacementStore) Upsert(_ context.Context, _ placement.Document) (int, error) {
	return 1, nil
}

func TestMeCVGetHandler_ReturnsPresentRefAndQuals(t *testing.T) {
	t.Parallel()
	profiles := &fakeCVProfiles{ref: placement.ProfileCV{FileID: "file_1", ContentURI: "gs://x", ContentHash: "h"}}
	store := &fakePlacementStore{doc: &placement.Document{
		Version:            2,
		Ready:              true,
		QualificationsText: "## Qualifications\nGo, Postgres, Kubernetes",
	}}
	h := httpmw.NewCandidateAuth(nil)(v1.MeCVGetHandler(v1.UploadDeps{
		Profiles:  profiles,
		Placement: &placement.Service{Store: store},
	}))
	req := httptest.NewRequest(http.MethodGet, "/me/cv", nil)
	req.Header.Set("X-Candidate-ID", "cand_cvget")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, true, out["ok"])
	require.Equal(t, true, out["present"])
	require.Equal(t, "file_1", out["file_id"])
	require.Equal(t, float64(2), out["cv_version"])
	require.Contains(t, out["extracted_text"], "Go")
}

func TestMeCVGetHandler_EmptyWhenNoData(t *testing.T) {
	t.Parallel()
	h := httpmw.NewCandidateAuth(nil)(v1.MeCVGetHandler(v1.UploadDeps{}))
	req := httptest.NewRequest(http.MethodGet, "/me/cv", nil)
	req.Header.Set("X-Candidate-ID", "cand_empty")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, false, out["present"])
}

// --- POST /me/chat (production agent path) ---

func TestMeChatAgentHandler_NilDepsIs503(t *testing.T) {
	t.Parallel()
	h := httpmw.NewCandidateAuth(nil)(v1.MeChatAgentHandler(nil))
	req := httptest.NewRequest(http.MethodPost, "/me/chat", strings.NewReader(`{"message":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Candidate-ID", "cand_chat")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, rec.Body.String(), "chat_agent_unavailable")
	require.Contains(t, rec.Body.String(), "can't process chat")
}

// --- apply details ---

type invFakeCandReader struct {
	sub domain.SubscriptionTier
	err error
}

func (f *invFakeCandReader) GetByID(_ context.Context, _ string) (*domain.CandidateProfile, error) {
	if f.err != nil {
		return nil, f.err
	}
	c := &domain.CandidateProfile{Subscription: f.sub}
	c.ID = "c"
	return c, nil
}

type invFakeApplyStore struct {
	id, slug, url, how string
	err                error
}

func (f *invFakeApplyStore) GetApplyDetails(_ context.Context, _ string) (string, string, string, string, error) {
	return f.id, f.slug, f.url, f.how, f.err
}

func TestApplyDetailsHandler_ActiveUnlocksHowToApply(t *testing.T) {
	t.Parallel()
	h := httpmw.NewCandidateAuth(nil)(v1.ApplyDetailsHandler(v1.ApplyDetailsDeps{
		Candidates: &invFakeCandReader{sub: domain.SubscriptionPaid},
		Store:      &invFakeApplyStore{id: "opp1", slug: "s1", url: "https://x", how: "Email hr@x"},
	}))
	req := httptest.NewRequest(http.MethodGet, "/me/opportunities/opp1/apply", nil)
	req.SetPathValue("id", "opp1")
	req.Header.Set("X-Candidate-ID", "c")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, "Email hr@x", out["how_to_apply"])
	require.NotEqual(t, true, out["locked"])
}

func TestApplyDetailsHandler_FreeIsLocked(t *testing.T) {
	t.Parallel()
	h := httpmw.NewCandidateAuth(nil)(v1.ApplyDetailsHandler(v1.ApplyDetailsDeps{
		Candidates: &invFakeCandReader{sub: domain.SubscriptionFree},
		Store:      &invFakeApplyStore{id: "opp1", slug: "s1", url: "https://x", how: "secret"},
	}))
	req := httptest.NewRequest(http.MethodGet, "/me/opportunities/opp1/apply", nil)
	req.SetPathValue("id", "opp1")
	req.Header.Set("X-Candidate-ID", "c")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, true, out["locked"])
}

func TestApplyDetailsHandler_NotFound(t *testing.T) {
	t.Parallel()
	h := httpmw.NewCandidateAuth(nil)(v1.ApplyDetailsHandler(v1.ApplyDetailsDeps{
		Candidates: &invFakeCandReader{sub: domain.SubscriptionPaid},
		Store:      &invFakeApplyStore{err: sql.ErrNoRows},
	}))
	req := httptest.NewRequest(http.MethodGet, "/me/opportunities/missing/apply", nil)
	req.SetPathValue("id", "missing")
	req.Header.Set("X-Candidate-ID", "c")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// --- billing lifecycle fail-closed / empty paths ---

func TestCancelHandler_NilCandidatesIs503(t *testing.T) {
	t.Parallel()
	h := httpmw.NewCandidateAuth(nil)(v1.CancelHandler(v1.BillingLifecycleDeps{}))
	req := httptest.NewRequest(http.MethodPost, "/billing/cancel", strings.NewReader(`{"reason":"too_expensive"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Candidate-ID", "c")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, rec.Body.String(), "unavailable")
}

func TestChangePlanHandler_NilCandidatesIs503(t *testing.T) {
	t.Parallel()
	h := httpmw.NewCandidateAuth(nil)(v1.ChangePlanHandler(v1.BillingLifecycleDeps{}))
	req := httptest.NewRequest(http.MethodPost, "/billing/change-plan", strings.NewReader(`{"plan_id":"managed"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Candidate-ID", "c")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestInvoicesHandler_NilStoreEmptyList(t *testing.T) {
	t.Parallel()
	h := httpmw.NewCandidateAuth(nil)(v1.InvoicesHandler(v1.BillingLifecycleDeps{}))
	req := httptest.NewRequest(http.MethodGet, "/billing/invoices", nil)
	req.Header.Set("X-Candidate-ID", "c")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var out []any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Empty(t, out)
}

func TestUsageHistoryHandler_NilMatchesEmptyList(t *testing.T) {
	t.Parallel()
	h := httpmw.NewCandidateAuth(nil)(v1.UsageHistoryHandler(nil))
	req := httptest.NewRequest(http.MethodGet, "/billing/usage-history", nil)
	req.Header.Set("X-Candidate-ID", "c")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var out []any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Empty(t, out)
}

type invStubSummary struct {
	q, d int
	err  error
}

func (s invStubSummary) SubscriptionSummary(_ context.Context, _ string) (int, int, error) {
	return s.q, s.d, s.err
}

func TestUsageHistoryHandler_WithSummary(t *testing.T) {
	t.Parallel()
	h := httpmw.NewCandidateAuth(nil)(v1.UsageHistoryHandler(invStubSummary{q: 3, d: 7}))
	req := httptest.NewRequest(http.MethodGet, "/billing/usage-history", nil)
	req.Header.Set("X-Candidate-ID", "c")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var out []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Len(t, out, 1)
	require.Equal(t, float64(7), out[0]["delivered"])
	require.Equal(t, float64(3), out[0]["queued"])
}
