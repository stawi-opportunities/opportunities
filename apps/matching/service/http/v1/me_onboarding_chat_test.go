package v1_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	v1 "github.com/stawi-opportunities/opportunities/apps/matching/service/http/v1"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

func TestOnboardingChatHandler_HeuristicReady(t *testing.T) {
	h := httpmw.NewCandidateAuth(nil)(v1.OnboardingChatHandler(v1.OnboardingChatDeps{}))
	body := `{"message":"I am a senior software engineer in Kenya, actively looking for full-time remote roles in Africa. English speaker."}`
	req := httptest.NewRequest(http.MethodPost, "/me/onboarding/chat", bytes.NewBufferString(body))
	req.Header.Set("X-Candidate-ID", "cand_chat_1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.NotEmpty(t, out["reply"])
	fields, ok := out["fields"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "senior", fields["experience_level"])
	require.Equal(t, "KE", fields["country"])
	require.Equal(t, "actively_looking", fields["job_search_status"])
	// regions default from country or explicit Africa
	regions, _ := fields["preferred_regions"].([]any)
	require.NotEmpty(t, regions)
	require.Equal(t, true, out["ready"], "missing=%v fields=%v", out["missing"], fields)
}

func TestOnboardingChatHandler_MergesDraftAcrossTurns(t *testing.T) {
	h := httpmw.NewCandidateAuth(nil)(v1.OnboardingChatHandler(v1.OnboardingChatDeps{}))
	// Turn 1: title only
	body1 := `{"message":"Looking for a data analyst role","draft":{}}`
	req1 := httptest.NewRequest(http.MethodPost, "/me/onboarding/chat", bytes.NewBufferString(body1))
	req1.Header.Set("X-Candidate-ID", "cand_chat_2")
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req1)
	require.Equal(t, http.StatusOK, rec1.Code)
	var out1 map[string]any
	require.NoError(t, json.Unmarshal(rec1.Body.Bytes(), &out1))
	require.Equal(t, false, out1["ready"])
	fields1, _ := out1["fields"].(map[string]any)

	// Turn 2: remaining details + prior draft
	draftJSON, _ := json.Marshal(map[string]any{"message": "Based in Uganda, senior, full-time, English, Africa, actively looking", "draft": fields1})
	req2 := httptest.NewRequest(http.MethodPost, "/me/onboarding/chat", bytes.NewReader(draftJSON))
	req2.Header.Set("X-Candidate-ID", "cand_chat_2")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusOK, rec2.Code, rec2.Body.String())
	var out2 map[string]any
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &out2))
	require.Equal(t, true, out2["ready"], "missing=%v", out2["missing"])
}

func TestOnboardingChatHandler_EmptyMessage(t *testing.T) {
	h := httpmw.NewCandidateAuth(nil)(v1.OnboardingChatHandler(v1.OnboardingChatDeps{}))
	req := httptest.NewRequest(http.MethodPost, "/me/onboarding/chat", bytes.NewBufferString(`{"message":"  "}`))
	req.Header.Set("X-Candidate-ID", "cand_chat_1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOnboardingChatHandler_FollowUpWhenMissing(t *testing.T) {
	h := httpmw.NewCandidateAuth(nil)(v1.OnboardingChatHandler(v1.OnboardingChatDeps{}))
	body := `{"message":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/me/onboarding/chat", bytes.NewBufferString(body))
	req.Header.Set("X-Candidate-ID", "cand_chat_1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, false, out["ready"])
	miss, _ := out["missing"].([]any)
	require.NotEmpty(t, miss)
}
