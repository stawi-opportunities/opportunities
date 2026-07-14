package v1_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	v1 "github.com/stawi-opportunities/opportunities/apps/matching/service/http/v1"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

func chatPOST(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/me/chat", bytes.NewBufferString(body))
	req.Header.Set("X-Candidate-ID", "cand_chat_1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// completeMsg is a single-turn free-text that should unlock plan selection:
// role, CV (capabilities), job types, salary, preferred countries, level.
const completeMsg = `CURRICULUM VITAE
Jane Doe — Senior Software Engineer
EXPERIENCE
2019-2024 Senior Engineer at Acme. Built distributed systems in Go.
EDUCATION
BSc Computer Science
SKILLS
Go, Kubernetes, PostgreSQL
Actively looking for full-time remote roles. Salary USD 90k-130k.
Opportunities from Kenya and Nigeria. English.`

func TestMeChatHandler_HeuristicReady(t *testing.T) {
	h := httpmw.NewCandidateAuth(nil)(v1.MeChatHandler(v1.MeChatDeps{}))
	rec := chatPOST(t, h, `{"message":`+jsonQuote(completeMsg)+`}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, true, out["ready"], "missing=%v fields=%v", out["missing"], out["fields"])
	fields := out["fields"].(map[string]any)
	require.Equal(t, "senior", fields["experience_level"])
	require.Equal(t, "actively_looking", fields["job_search_status"])
	require.NotNil(t, fields["salary_min"])
	countries, _ := fields["preferred_countries"].([]any)
	require.NotEmpty(t, countries)
	require.Contains(t, out, "field_status")
	fs := out["field_status"].(map[string]any)
	require.Equal(t, true, fs["capabilities"].(map[string]any)["ok"])
	require.Equal(t, true, fs["salary_expectation"].(map[string]any)["ok"])
	require.Equal(t, true, fs["preferred_countries"].(map[string]any)["ok"])
	require.Equal(t, true, fs["job_types"].(map[string]any)["ok"])
}

func TestMeChatHandler_DoesNotInventTitleFromActivelyLooking(t *testing.T) {
	h := httpmw.NewCandidateAuth(nil)(v1.MeChatHandler(v1.MeChatDeps{}))
	body := `{"message":"I am actively looking for full-time remote roles"}`
	rec := chatPOST(t, h, body)
	require.Equal(t, http.StatusOK, rec.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, false, out["ready"])
	miss, _ := out["missing"].([]any)
	require.Contains(t, miss, "target_job_title")
	fields := out["fields"].(map[string]any)
	if title, _ := fields["target_job_title"].(string); title != "" {
		require.NotContains(t, stringsToLower(title), "full-time")
		require.NotContains(t, stringsToLower(title), "remote roles")
	}
}

func TestMeChatHandler_RequiresCVAndSalary(t *testing.T) {
	h := httpmw.NewCandidateAuth(nil)(v1.MeChatHandler(v1.MeChatDeps{}))
	// Role + geo + job type but no CV and no salary → not ready.
	// LinkedIn alone must NOT unlock capabilities.
	body := `{"message":"I am a senior software engineer in Kenya looking for full-time roles. LinkedIn linkedin.com/in/jane"}`
	rec := chatPOST(t, h, body)
	require.Equal(t, http.StatusOK, rec.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, false, out["ready"])
	miss, _ := out["missing"].([]any)
	require.Contains(t, miss, "capabilities")
	require.Contains(t, miss, "salary_expectation")
}

func TestMeChatHandler_CVCountsAsCapabilities(t *testing.T) {
	h := httpmw.NewCandidateAuth(nil)(v1.MeChatHandler(v1.MeChatDeps{}))
	cv := `CURRICULUM VITAE
Jane Doe — Senior Backend Engineer
EXPERIENCE
2020-2024 Senior Engineer at Acme Corp. Led payments platform, Go and PostgreSQL.
EDUCATION
BSc Computer Science
SKILLS
Go, distributed systems, Kubernetes
Looking for full-time roles, salary USD 100000, opportunities in Kenya and US.`
	rec := chatPOST(t, h, `{"message":`+jsonQuote(cv)+`}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	fs := out["field_status"].(map[string]any)
	require.Equal(t, true, fs["capabilities"].(map[string]any)["ok"], "fields=%v", out["fields"])
	require.Equal(t, true, fs["salary_expectation"].(map[string]any)["ok"])
}

func stringsToLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestMeChatHandler_MergesDraftAcrossTurns(t *testing.T) {
	h := httpmw.NewCandidateAuth(nil)(v1.MeChatHandler(v1.MeChatDeps{}))
	rec1 := chatPOST(t, h, `{"message":"Looking for a data analyst role","draft":{}}`)
	require.Equal(t, http.StatusOK, rec1.Code)
	var out1 map[string]any
	require.NoError(t, json.Unmarshal(rec1.Body.Bytes(), &out1))
	require.Equal(t, false, out1["ready"])
	fields1 := out1["fields"].(map[string]any)

	payload := map[string]any{
		"message": "Senior level, full-time. Salary KES 250000. Opportunities from Uganda. Actively looking.\n\nCURRICULUM VITAE\nData Analyst\nEXPERIENCE\n3 years analytics at Acme.\nEDUCATION\nBSc Stats\nSKILLS\nSQL, Python",
		"draft":   fields1,
		"history": []map[string]string{
			{"role": "user", "content": "Looking for a data analyst role"},
			{"role": "assistant", "content": "Please attach or paste your CV."},
		},
	}
	raw, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/me/chat", bytes.NewReader(raw))
	req.Header.Set("X-Candidate-ID", "cand_chat_1")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	require.Equal(t, http.StatusOK, rec2.Code, rec2.Body.String())
	var out2 map[string]any
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &out2))
	require.Equal(t, true, out2["ready"], "missing=%v fields=%v", out2["missing"], out2["fields"])
	fields2 := out2["fields"].(map[string]any)
	require.Equal(t, "senior", fields2["experience_level"])
	title, _ := fields2["target_job_title"].(string)
	require.Contains(t, stringsToLower(title), "data analyst")
	countries, _ := fields2["preferred_countries"].([]any)
	require.NotEmpty(t, countries)
}

func TestMeChatHandler_ReadyFromHistoryCorpusWithoutDraft(t *testing.T) {
	h := httpmw.NewCandidateAuth(nil)(v1.MeChatHandler(v1.MeChatDeps{}))
	payload := map[string]any{
		"message": "Also full-time, salary USD 95k, opportunities from Kenya.\n\nCURRICULUM VITAE\nSenior Software Engineer\nEXPERIENCE\nBuilt APIs for 6 years.\nEDUCATION\nBSc CS\nSKILLS\nGo, Postgres",
		"draft":   map[string]any{},
		"history": []map[string]string{
			{"role": "user", "content": "I am a senior software engineer based in Kenya"},
			{"role": "assistant", "content": "Please attach your CV and salary expectations."},
		},
	}
	raw, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/me/chat", bytes.NewReader(raw))
	req.Header.Set("X-Candidate-ID", "cand_chat_1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, true, out["ready"], "missing=%v fields=%v", out["missing"], out["fields"])
	fields := out["fields"].(map[string]any)
	require.Equal(t, "senior", fields["experience_level"])
	title, _ := fields["target_job_title"].(string)
	require.Contains(t, stringsToLower(title), "software engineer")
}

func TestMeChatHandler_EmptyMessage(t *testing.T) {
	h := httpmw.NewCandidateAuth(nil)(v1.MeChatHandler(v1.MeChatDeps{}))
	rec := chatPOST(t, h, `{"message":"  "}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestMeChatHandler_LinkedInDoesNotSatisfyCapabilities(t *testing.T) {
	h := httpmw.NewCandidateAuth(nil)(v1.MeChatHandler(v1.MeChatDeps{}))
	// LinkedIn alone must not mark capabilities OK.
	body := `{"message":"","linkedin":"linkedin.com/in/janedoe","draft":{"target_job_title":"Engineer"}}`
	rec := chatPOST(t, h, body)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	fields := out["fields"].(map[string]any)
	require.NotEmpty(t, fields["linkedin"])
	fs := out["field_status"].(map[string]any)
	require.Equal(t, false, fs["capabilities"].(map[string]any)["ok"])
}

func TestMeChatHandler_StructuredCVText(t *testing.T) {
	h := httpmw.NewCandidateAuth(nil)(v1.MeChatHandler(v1.MeChatDeps{}))
	cv := `CURRICULUM VITAE
Jane Doe — Senior Backend Engineer
EXPERIENCE
2020-2024 Senior Engineer at Acme. Built Go services.
EDUCATION
BSc Computer Science
SKILLS
Go, Kubernetes, PostgreSQL`
	payload := map[string]any{
		"message":     "",
		"cv_text":     cv,
		"cv_filename": "jane-cv.txt",
		"draft":       map[string]any{},
	}
	raw, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/me/chat", bytes.NewReader(raw))
	req.Header.Set("X-Candidate-ID", "cand_chat_1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	fs := out["field_status"].(map[string]any)
	require.Equal(t, true, fs["capabilities"].(map[string]any)["ok"], "fields=%v", out["fields"])
}

func TestMeChatHandler_FollowUpWhenMissing(t *testing.T) {
	h := httpmw.NewCandidateAuth(nil)(v1.MeChatHandler(v1.MeChatDeps{}))
	rec := chatPOST(t, h, `{"message":"hello"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, false, out["ready"])
	miss, _ := out["missing"].([]any)
	require.NotEmpty(t, miss)
	require.NotEmpty(t, out["reply"])
}

func TestMeChatHandler_ExtractsAndAsksForMissing(t *testing.T) {
	h := httpmw.NewCandidateAuth(nil)(v1.MeChatHandler(v1.MeChatDeps{}))
	body := `{"message":"I'm a senior software engineer looking for full-time remote roles. Salary USD 90k-120k. Opportunities from Kenya and Nigeria."}`
	rec := chatPOST(t, h, body)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, false, out["ready"])
	fields, _ := out["fields"].(map[string]any)
	require.NotEmpty(t, fields["target_job_title"], "fields=%v", fields)
	require.NotEmpty(t, fields["experience_level"], "fields=%v", fields)
	miss, _ := out["missing"].([]any)
	require.Contains(t, miss, "capabilities")
	reply, _ := out["reply"].(string)
	require.NotEmpty(t, reply)
	low := strings.ToLower(reply)
	require.True(t,
		strings.Contains(low, "cv") || strings.Contains(low, "resume"),
		"reply should ask for CV: %s", reply)
	// Acknowledge what was extracted so the seeker knows we processed input.
	require.True(t,
		strings.Contains(low, "got it") || strings.Contains(low, "engineer") || strings.Contains(low, "senior"),
		"reply should acknowledge known data: %s", reply)
}

func TestMeChatHandler_LLMReplyOverriddenWhenNotReady(t *testing.T) {
	llm := stubLLM(`{"fields":{"target_job_title":"Nurse"},"reply":"You're all set to pick a plan!"}`)
	h := httpmw.NewCandidateAuth(nil)(v1.MeChatHandler(v1.MeChatDeps{LLM: llm}))
	rec := chatPOST(t, h, `{"message":"I want nursing jobs"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, false, out["ready"])
	reply, _ := out["reply"].(string)
	require.NotContains(t, stringsToLower(reply), "pick a plan")
}

func TestMeChatHandler_LLMFieldsMergedAndAssessed(t *testing.T) {
	llm := stubLLM(`{
	  "fields": {
	    "target_job_title": "Product Manager",
	    "experience_level": "mid",
	    "job_search_status": "actively_looking",
	    "country": "KE",
	    "preferred_countries": ["KE", "NG"],
	    "preferred_regions": ["Africa"],
	    "preferred_languages": ["English"],
	    "job_types": ["Full-time"],
	    "salary_min": 80000,
	    "salary_max": 120000,
	    "currency": "USD",
	    "extra_info": "CURRICULUM VITAE\nProduct Manager\nEXPERIENCE\n5 years PM at Acme.\nEDUCATION\nMBA\nSKILLS\nRoadmaps, research"
	  },
	  "reply": "Perfect — ready for a plan."
	}`)
	h := httpmw.NewCandidateAuth(nil)(v1.MeChatHandler(v1.MeChatDeps{LLM: llm}))
	rec := chatPOST(t, h, `{"message":"PM in Kenya"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, true, out["ready"], "missing=%v fields=%v", out["missing"], out["fields"])
	require.Equal(t, "llm", out["source"])
	fields := out["fields"].(map[string]any)
	require.Equal(t, "Product Manager", fields["target_job_title"])
	require.Equal(t, "KE", fields["country"])
}

func TestMeChatHandler_PersistsConversationAndResumes(t *testing.T) {
	store := &fakeDraftStore{}
	h := httpmw.NewCandidateAuth(nil)(v1.MeChatHandler(v1.MeChatDeps{Drafts: store}))

	rec1 := chatPOST(t, h, `{"message":"I am a senior data analyst"}`)
	require.Equal(t, http.StatusOK, rec1.Code, rec1.Body.String())
	require.Equal(t, 1, store.setCalls)
	var out1 map[string]any
	require.NoError(t, json.Unmarshal(rec1.Body.Bytes(), &out1))
	msgs1, ok := out1["messages"].([]any)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(msgs1), 2)
	require.Equal(t, false, out1["ready"])

	store.getResp = store.setBody
	rec2 := chatPOST(t, h, `{"message":"Full-time, salary USD 70k, opportunities from Kenya, actively looking.\n\nCURRICULUM VITAE\nSenior Data Analyst\nEXPERIENCE\n5 years analytics.\nEDUCATION\nBSc\nSKILLS\nSQL"}`)
	require.Equal(t, http.StatusOK, rec2.Code, rec2.Body.String())
	require.Equal(t, 2, store.setCalls)
	var out2 map[string]any
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &out2))
	require.Equal(t, true, out2["ready"], "missing=%v fields=%v", out2["missing"], out2["fields"])
	fields := out2["fields"].(map[string]any)
	title, _ := fields["target_job_title"].(string)
	require.Contains(t, stringsToLower(title), "data analyst")
	msgs2 := out2["messages"].([]any)
	require.GreaterOrEqual(t, len(msgs2), 4)

	var env map[string]any
	require.NoError(t, json.Unmarshal(store.setBody, &env))
	require.Contains(t, env, "messages")
	require.Contains(t, env, "fields")
}

func TestOnboardingHandler_PutPreservesMessagesWhenOmitted(t *testing.T) {
	prior := []byte(`{"step":1,"fields":{"target_job_title":"Nurse"},"messages":[{"role":"user","content":"I am a nurse"},{"role":"assistant","content":"Which country?"}]}`)
	store := &fakeDraftStore{getResp: prior}
	h := httpmw.NewCandidateAuth(nil)(v1.OnboardingHandler(v1.OnboardingDeps{Drafts: store}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, reqWithCandidate(t, http.MethodPut, "/me/onboarding",
		`{"step":2,"fields":{"target_job_title":"Nurse","country":"KE"}}`))
	require.Equal(t, http.StatusNoContent, rec.Code)
	var env map[string]any
	require.NoError(t, json.Unmarshal(store.setBody, &env))
	msgs, ok := env["messages"].([]any)
	require.True(t, ok)
	require.Len(t, msgs, 2)
}

type stubLLM string

func (s stubLLM) Complete(_ context.Context, _ string) (string, error) {
	return string(s), nil
}
