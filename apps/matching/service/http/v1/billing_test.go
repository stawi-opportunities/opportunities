package v1_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	v1 "github.com/stawi-opportunities/opportunities/apps/matching/service/http/v1"
	"github.com/stawi-opportunities/opportunities/pkg/billing"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

// --- GET /billing/plans ------------------------------------------------

func TestPlansHandler_ReturnsCatalogAndRoute(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/billing/plans", nil)
	req.Header.Set("CF-IPCountry", "KE")
	rec := httptest.NewRecorder()
	v1.PlansHandler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Country string `json:"country"`
		Route   string `json:"route"`
		Plans   []struct {
			ID       string `json:"id"`
			USDCents int    `json:"usd_cents"`
		} `json:"plans"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "KE", body.Country)
	require.Equal(t, "FLUTTERWAVE", body.Route)
	require.Len(t, body.Plans, 3)
}

// --- POST /billing/checkout --------------------------------------------

type stubGateway struct {
	createResult billing.CheckoutResult
	createErr    error
	statusResult billing.StatusResult
	statusErr    error
	lastReq      billing.CheckoutRequest
}

func (g *stubGateway) CreateCheckout(_ context.Context, req billing.CheckoutRequest) (billing.CheckoutResult, error) {
	g.lastReq = req
	return g.createResult, g.createErr
}

func (g *stubGateway) CheckoutStatus(_ context.Context, _ string) (billing.StatusResult, error) {
	return g.statusResult, g.statusErr
}

func candReq(t *testing.T, method, target, candidateID, body string) *http.Request {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	r.Header.Set("X-Candidate-ID", candidateID)
	return r
}

func TestCheckoutHandler_ValidPlanReturnsGatewayResult(t *testing.T) {
	t.Parallel()
	gw := &stubGateway{createResult: billing.CheckoutResult{
		Status: billing.StatusRedirect, Route: billing.RouteFlutterwave,
		PromptID: "chk_1", RedirectURL: "https://pay/abc",
	}}
	h := httpmw.NewCandidateAuth(nil)(v1.CheckoutHandler(v1.CheckoutDeps{Gateway: gw}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, candReq(t, http.MethodPost, "/billing/checkout", "cand_1", `{"plan_id":"pro"}`))

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Status      string `json:"status"`
		Route       string `json:"route"`
		PromptID    string `json:"prompt_id"`
		RedirectURL string `json:"redirect_url"`
		Amount      int    `json:"amount"`
		PlanID      string `json:"plan_id"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "redirect", resp.Status)
	require.Equal(t, "chk_1", resp.PromptID)
	require.Equal(t, "https://pay/abc", resp.RedirectURL)
	require.Equal(t, "pro", resp.PlanID)
	require.Equal(t, "cand_1", gw.lastReq.CandidateID)
}

func TestCheckoutHandler_RejectsUnknownPlan(t *testing.T) {
	t.Parallel()
	gw := &stubGateway{}
	h := httpmw.NewCandidateAuth(nil)(v1.CheckoutHandler(v1.CheckoutDeps{Gateway: gw}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, candReq(t, http.MethodPost, "/billing/checkout", "cand_1", `{"plan_id":"free"}`))

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid_plan")
}

func TestCheckoutHandler_GatewayUnavailableIs503(t *testing.T) {
	t.Parallel()
	gw := &stubGateway{createErr: billing.ErrGatewayUnavailable}
	h := httpmw.NewCandidateAuth(nil)(v1.CheckoutHandler(v1.CheckoutDeps{Gateway: gw}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, candReq(t, http.MethodPost, "/billing/checkout", "cand_1", `{"plan_id":"starter"}`))

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, rec.Body.String(), "billing_unavailable")
}

// --- GET /billing/checkout/status --------------------------------------

func TestCheckoutStatusHandler_RequiresPromptID(t *testing.T) {
	t.Parallel()
	gw := &stubGateway{}
	h := httpmw.NewCandidateAuth(nil)(v1.CheckoutStatusHandler(v1.CheckoutStatusDeps{Gateway: gw}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, candReq(t, http.MethodGet, "/billing/checkout/status", "cand_1", ""))

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "missing_prompt_id")
}

func TestCheckoutStatusHandler_NoStoreIs503(t *testing.T) {
	t.Parallel()
	// Without a Store the handler cannot enforce ownership, so it must refuse
	// rather than poll the gateway by a guessable prompt_id — otherwise any
	// candidate could read another's checkout (IDOR).
	gw := &stubGateway{statusResult: billing.StatusResult{Status: billing.StatusPaid, SubscriptionID: "sub-1"}}
	h := httpmw.NewCandidateAuth(nil)(v1.CheckoutStatusHandler(v1.CheckoutStatusDeps{Gateway: gw}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, candReq(t, http.MethodGet, "/billing/checkout/status?prompt_id=chk_1", "cand_1", ""))

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, rec.Body.String(), "status_unavailable")
}

// --- POST /billing/webhook ---------------------------------------------

func TestWebhookHandler_RejectsBadSignature(t *testing.T) {
	t.Parallel()
	h := v1.WebhookHandler(v1.WebhookDeps{Secret: "topsecret"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/billing/webhook", strings.NewReader(`{"prompt_id":"x","status":"SUCCESSFUL"}`))
	req.Header.Set("X-Payment-Signature", "sha256=deadbeef")
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid_signature")
}

func TestWebhookHandler_NoSecretRefuses(t *testing.T) {
	t.Parallel()
	// A webhook with no configured secret cannot authenticate the caller and
	// must fail closed — it activates paid subscriptions, so it must never
	// process an unsigned request.
	h := v1.WebhookHandler(v1.WebhookDeps{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/billing/webhook", strings.NewReader(`{"prompt_id":"x","status":"SUCCESSFUL"}`))
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Contains(t, rec.Body.String(), "webhook_not_configured")
}

func TestWebhookHandler_ValidSignatureAccepts(t *testing.T) {
	t.Parallel()
	const secret = "topsecret"
	body := `{"prompt_id":"x","status":"SUCCESSFUL"}`
	h := v1.WebhookHandler(v1.WebhookDeps{Secret: secret})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/billing/webhook", strings.NewReader(body))
	req.Header.Set("X-Payment-Signature", "sha256="+webhookSig(secret, body))
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"ok":true`)
}

func TestWebhookHandler_MissingPromptIDIs400(t *testing.T) {
	t.Parallel()
	const secret = "topsecret"
	body := `{"status":"SUCCESSFUL"}`
	h := v1.WebhookHandler(v1.WebhookDeps{Secret: secret})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/billing/webhook", strings.NewReader(body))
	req.Header.Set("X-Payment-Signature", "sha256="+webhookSig(secret, body))
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "missing_prompt_id")
}

// webhookSig computes the HMAC-SHA256 hex digest the webhook verifies.
func webhookSig(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}
