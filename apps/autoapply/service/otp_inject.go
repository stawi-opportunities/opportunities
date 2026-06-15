package service

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/autoapply/otprendezvous"
)

// otpInjectRequest is the JSON body for POST /internal/otp.
type otpInjectRequest struct {
	Email   string `json:"email"`
	Company string `json:"company"`
	Code    string `json:"code"`
}

// NewOTPInjectHandler returns the manual OTP injector endpoint. A human
// reads the emailed security code from the candidate's inbox and POSTs it
// here; the handler computes the same rendezvous key the Greenhouse
// submitter is polling and Puts the code, unblocking the held browser.
//
// This is the Phase-2 stand-in for the Phase-3 inbound-email webhook:
// identical Put + key, just a human as the data source. Guarded by a
// shared secret — callers must not mount the route when the secret is
// empty or the rendezvous is nil.
func NewOTPInjectHandler(rdv otprendezvous.Rendezvous, secret string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !validOTPSecret(r, secret) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var req otpInjectRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&req); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		req.Email = strings.TrimSpace(req.Email)
		req.Company = strings.TrimSpace(req.Company)
		req.Code = strings.TrimSpace(req.Code)
		if req.Email == "" || req.Company == "" || req.Code == "" {
			http.Error(w, "email, company, code are required", http.StatusBadRequest)
			return
		}

		// Key() normalises both inputs, so the submitter (company from
		// the apply URL) and this caller (company typed by a human) agree.
		key := otprendezvous.Key(req.Email, req.Company)

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := rdv.Put(ctx, key, req.Code); err != nil {
			http.Error(w, "put: "+err.Error(), http.StatusBadGateway)
			return
		}

		util.Log(r.Context()).
			WithField("email", req.Email).
			WithField("company", otprendezvous.NormCompany(req.Company)).
			Info("autoapply: OTP code injected via /internal/otp")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"accepted"}`))
	})
}

// validOTPSecret constant-time compares the request's secret (either the
// X-OTP-Secret header or a Bearer token) against the configured secret.
func validOTPSecret(r *http.Request, secret string) bool {
	if secret == "" {
		return false
	}
	got := r.Header.Get("X-OTP-Secret")
	if got == "" {
		got = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(secret)) == 1
}
