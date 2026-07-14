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
	require.Equal(t, true, out["ready"])
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
