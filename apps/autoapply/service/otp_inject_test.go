package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/autoapply/otprendezvous"
)

const testSecret = "s3cret"

func postOTP(t *testing.T, h http.Handler, body, secret string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/internal/otp", strings.NewReader(body))
	if secret != "" {
		r.Header.Set("X-OTP-Secret", secret)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestOTPInjectSuccess(t *testing.T) {
	rdv := otprendezvous.NewInMemory()
	h := NewOTPInjectHandler(rdv, testSecret)

	body := `{"email":"joakim@example.com","company":"Cloudflare, Inc.","code":"51iL5qrq"}`
	w := postOTP(t, h, body, testSecret)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", w.Code, w.Body.String())
	}

	// The submitter-side key must resolve to the just-injected code.
	key := otprendezvous.Key("joakim@example.com", "cloudflare")
	got, err := rdv.Poll(context.Background(), key, time.Second)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if got != "51iL5qrq" {
		t.Fatalf("code = %q, want 51iL5qrq", got)
	}
}

func TestOTPInjectUnauthorized(t *testing.T) {
	h := NewOTPInjectHandler(otprendezvous.NewInMemory(), testSecret)

	if w := postOTP(t, h, `{}`, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no secret: status = %d, want 401", w.Code)
	}
	if w := postOTP(t, h, `{}`, "wrong"); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong secret: status = %d, want 401", w.Code)
	}
}

func TestOTPInjectValidation(t *testing.T) {
	h := NewOTPInjectHandler(otprendezvous.NewInMemory(), testSecret)

	if w := postOTP(t, h, `not json`, testSecret); w.Code != http.StatusBadRequest {
		t.Fatalf("bad json: status = %d, want 400", w.Code)
	}
	if w := postOTP(t, h, `{"email":"a@b.com","code":"x"}`, testSecret); w.Code != http.StatusBadRequest {
		t.Fatalf("missing company: status = %d, want 400", w.Code)
	}
}
