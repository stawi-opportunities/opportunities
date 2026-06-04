package v1

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/billing"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

// WebhookDeps bundles what the payment webhook needs.
type WebhookDeps struct {
	Activator *billing.Activator
	// Secret is the HMAC-SHA256 key for verifying the X-Payment-Signature
	// header over the raw request body. It is MANDATORY: a webhook that can
	// activate paid subscriptions must never fail open. If empty, the
	// handler refuses every request (500) — and main.go declines to register
	// the route at all (and fails boot when billing is otherwise enabled).
	Secret string
}

// webhookPayload is the minimal shape we read from a service-payment
// completion callback. service-payment's webhook contract isn't pinned in
// the generated protos (it's an HTTP push, not an RPC), so we parse a
// permissive JSON envelope: the prompt/transaction id plus a status. Both
// snake_case and the provider's likely field names are accepted.
type webhookPayload struct {
	PromptID       string `json:"prompt_id"`
	TransactionID  string `json:"transaction_id"`
	ID             string `json:"id"`
	Status         string `json:"status"`
	State          string `json:"state"`
	SubscriptionID string `json:"subscription_id"`
	ExternalID     string `json:"external_id"`
	Error          string `json:"error"`
}

// WebhookHandler serves POST /billing/webhook — the unauthenticated
// endpoint service-payment calls when a payment completes. It is NOT
// JWT-gated (the provider has no candidate token); instead it verifies an
// optional HMAC signature over the body. On a confirmed payment it drives
// the same idempotent Activator the status poller and reconciler use, so
// double-delivery is safe.
func WebhookHandler(deps WebhookDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			httpmw.ProblemJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
			return
		}
		ctx := r.Context()
		log := util.Log(ctx)

		body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
		if err != nil {
			httpmw.ProblemJSON(w, http.StatusBadRequest, "body_read_failed", "could not read request body")
			return
		}
		// Fail closed: without a configured secret we cannot authenticate the
		// caller, and this endpoint flips subscriptions to paid — never serve
		// it unauthenticated.
		if deps.Secret == "" {
			log.Error("billing/webhook: BILLING_WEBHOOK_SECRET not configured; refusing request")
			httpmw.ProblemJSON(w, http.StatusInternalServerError, "webhook_not_configured", "webhook is not configured")
			return
		}
		if !validSignature(deps.Secret, r.Header.Get("X-Payment-Signature"), body) {
			log.Warn("billing/webhook: signature verification failed")
			httpmw.ProblemJSON(w, http.StatusUnauthorized, "invalid_signature", "signature verification failed")
			return
		}

		var in webhookPayload
		if err := json.Unmarshal(body, &in); err != nil {
			httpmw.ProblemJSON(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
			return
		}
		promptID := firstNonEmpty(in.PromptID, in.TransactionID, in.ID)
		if promptID == "" {
			httpmw.ProblemJSON(w, http.StatusBadRequest, "missing_prompt_id", "no prompt/transaction id in payload")
			return
		}
		subID := firstNonEmpty(in.SubscriptionID, in.ExternalID)
		status := normalizeWebhookStatus(in.Status, in.State)

		if deps.Activator != nil {
			if actErr := deps.Activator.Activate(ctx, promptID, status, subID, in.Error); actErr != nil {
				log.WithError(actErr).WithField("prompt_id", promptID).Error("billing/webhook: activation failed")
				httpmw.ProblemJSON(w, http.StatusBadGateway, "activation_failed", "could not apply payment")
				return
			}
		}
		// Always 200 on a well-formed, verified webhook so the provider
		// stops retrying — activation is idempotent and the reconciler
		// covers any drop.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}
}

// normalizeWebhookStatus folds the provider's status/state strings onto our
// lifecycle. Accepts both the common STATUS names (SUCCESSFUL/FAILED) and
// plain words (paid/success/failed/cancelled).
func normalizeWebhookStatus(status, state string) billing.Status {
	s := strings.ToUpper(strings.TrimSpace(status))
	if s == "" {
		s = strings.ToUpper(strings.TrimSpace(state))
	}
	switch s {
	case "SUCCESSFUL", "SUCCESS", "PAID", "COMPLETED", "ACTIVE":
		return billing.StatusPaid
	case "FAILED", "CANCELLED", "CANCELED", "DECLINED", "ERROR":
		return billing.StatusFailed
	default:
		return billing.StatusPending
	}
}

// validSignature checks an HMAC-SHA256 hex signature over body. The header
// may be "sha256=<hex>" or the bare hex digest.
func validSignature(secret, header string, body []byte) bool {
	got := strings.TrimSpace(header)
	got = strings.TrimPrefix(got, "sha256=")
	if got == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(strings.ToLower(got)), []byte(want))
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
