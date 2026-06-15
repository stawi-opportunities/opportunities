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

// the real forwarded Greenhouse OTP email, as raw RFC822.
const sampleEmail = "From: Greenhouse <no-reply@us.greenhouse-mail.io>\r\n" +
	"To: joakim@example.com\r\n" +
	"Subject: Security code for your application to Cloudflare\r\n" +
	"Message-Id: <abc123@greenhouse>\r\n" +
	"Content-Type: text/plain; charset=UTF-8\r\n" +
	"\r\n" +
	"Hi Joakim,\r\n\r\n" +
	"Copy and paste this code into the security code field on your application:\r\n\r\n" +
	"51iL5qrq\r\n\r\n" +
	"After you enter the code, resubmit your application.\r\n"

func postEmail(t *testing.T, h http.Handler, body, secret string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/webhooks/otp", strings.NewReader(body))
	if secret != "" {
		r.Header.Set("X-OTP-Secret", secret)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestOTPWebhookDeliversCode(t *testing.T) {
	rdv := otprendezvous.NewInMemory()
	h := NewOTPWebhookHandler(rdv, testSecret, "greenhouse-mail.io")

	w := postEmail(t, h, sampleEmail, testSecret)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", w.Code, w.Body.String())
	}

	// The code must be retrievable under the submitter-side key: candidate
	// email (the To header) + company (from the subject).
	key := otprendezvous.Key("joakim@example.com", "cloudflare")
	got, err := rdv.Poll(context.Background(), key, time.Second)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if got != "51iL5qrq" {
		t.Fatalf("code = %q, want 51iL5qrq", got)
	}
}

func TestOTPWebhookRejectsSenderDomain(t *testing.T) {
	rdv := otprendezvous.NewInMemory()
	h := NewOTPWebhookHandler(rdv, testSecret, "greenhouse-mail.io")

	spoofed := strings.Replace(sampleEmail,
		"no-reply@us.greenhouse-mail.io", "attacker@evil.example", 1)
	w := postEmail(t, h, spoofed, testSecret)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (acked, ignored)", w.Code)
	}
	// Nothing should have been delivered.
	key := otprendezvous.Key("joakim@example.com", "cloudflare")
	if _, err := rdv.Poll(context.Background(), key, 50*time.Millisecond); err == nil {
		t.Fatal("spoofed sender must not deliver a code")
	}
}

func TestOTPWebhookUnauthorized(t *testing.T) {
	h := NewOTPWebhookHandler(otprendezvous.NewInMemory(), testSecret, "greenhouse-mail.io")
	if w := postEmail(t, h, sampleEmail, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestOTPWebhookDedupRedelivery(t *testing.T) {
	rdv := otprendezvous.NewInMemory()
	h := NewOTPWebhookHandler(rdv, testSecret, "greenhouse-mail.io")

	// First delivery lands.
	postEmail(t, h, sampleEmail, testSecret)
	key := otprendezvous.Key("joakim@example.com", "cloudflare")
	if _, err := rdv.Poll(context.Background(), key, time.Second); err != nil {
		t.Fatalf("first delivery Poll: %v", err)
	}
	// Redelivery of the same Message-Id must be dropped, so no code is
	// re-Put for the (already consumed) key.
	postEmail(t, h, sampleEmail, testSecret)
	if _, err := rdv.Poll(context.Background(), key, 50*time.Millisecond); err == nil {
		t.Fatal("redelivery must be deduped, not re-Put")
	}
}
