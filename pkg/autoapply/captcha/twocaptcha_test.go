package captcha

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/autoapply/browser"
)

func newTestSolver(t *testing.T, h http.HandlerFunc) *TwoCaptcha {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := NewTwoCaptcha("test-key")
	c.baseURL = srv.URL
	c.poll = time.Millisecond
	return c
}

func TestTwoCaptchaSolveEnterprise(t *testing.T) {
	var gotType string
	var gotPayload map[string]any
	calls := 0

	c := newTestSolver(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/createTask":
			var body struct {
				Task map[string]any `json:"task"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotType, _ = body.Task["type"].(string)
			gotPayload, _ = body.Task["enterprisePayload"].(map[string]any)
			_ = json.NewEncoder(w).Encode(map[string]any{"errorId": 0, "taskId": 123})
		case "/getTaskResult":
			calls++
			if calls < 2 {
				_ = json.NewEncoder(w).Encode(map[string]any{"errorId": 0, "status": "processing"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"errorId":  0,
				"status":   "ready",
				"solution": map[string]any{"gRecaptchaResponse": "TOKEN123"},
			})
		}
	})

	tok, err := c.Solve(context.Background(), browser.CaptchaTask{
		Type:    browser.CaptchaRecaptchaV2Enterprise,
		SiteKey: "sk",
		PageURL: "https://boards.greenhouse.io/x",
		Data:    "s-value",
		Action:  "submit",
	})
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if tok != "TOKEN123" {
		t.Fatalf("token = %q, want TOKEN123", tok)
	}
	if gotType != "RecaptchaV2EnterpriseTaskProxyless" {
		t.Fatalf("task type = %q", gotType)
	}
	if gotPayload["s"] != "s-value" {
		t.Fatalf("enterprisePayload.s = %v, want s-value", gotPayload["s"])
	}
}

func TestTwoCaptchaSolveTurnstileTokenField(t *testing.T) {
	c := newTestSolver(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/createTask":
			var body struct {
				Task map[string]any `json:"task"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Task["type"] != "TurnstileTaskProxyless" {
				t.Errorf("type = %v, want TurnstileTaskProxyless", body.Task["type"])
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"errorId": 0, "taskId": 1})
		case "/getTaskResult":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"errorId":  0,
				"status":   "ready",
				"solution": map[string]any{"token": "cf-token"},
			})
		}
	})

	tok, err := c.Solve(context.Background(), browser.CaptchaTask{
		Type: browser.CaptchaTurnstile, SiteKey: "sk", PageURL: "https://x",
	})
	if err != nil || tok != "cf-token" {
		t.Fatalf("Solve = (%q, %v), want (cf-token, nil)", tok, err)
	}
}

func TestTwoCaptchaErrorFromAPI(t *testing.T) {
	c := newTestSolver(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"errorId": 1, "errorCode": "ERROR_KEY_DOES_NOT_EXIST",
		})
	})
	if _, err := c.Solve(context.Background(), browser.CaptchaTask{
		Type: browser.CaptchaHCaptcha, SiteKey: "sk", PageURL: "https://x",
	}); err == nil {
		t.Fatal("expected error from createTask errorId != 0")
	}
}

func TestTwoCaptchaMissingKey(t *testing.T) {
	c := NewTwoCaptcha("")
	if _, err := c.Solve(context.Background(), browser.CaptchaTask{Type: browser.CaptchaHCaptcha}); err == nil {
		t.Fatal("expected error for empty API key")
	}
}
