package billing

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pitabwire/util"
)

// DusuPayConfig holds everything an ops operator sets for the
// DusuPay adapter. All fields are required; empty values disable
// the provider (the router returns ErrNoProvider to African users).
//
// BaseURL is https://api.dusupay.com for production and
// https://sandbox.dusupay.com for staging. The adapter doesn't pick
// an environment implicitly — ops sets the right URL explicitly.
type DusuPayConfig struct {
	BaseURL       string
	APIKey        string // Bearer token for calls OUT to DusuPay
	WebhookSecret string // HMAC-SHA256 secret for calls IN from DusuPay
	// CallbackURL / RedirectURL are absolute URLs DusuPay returns the
	// user to after payment completes / fails.
	CallbackURL string
	// MerchantCode identifies this stawi.jobs account in DusuPay.
	MerchantCode string
}

// DusuPay implements Provider against DusuPay's collection API.
type DusuPay struct {
	cfg    DusuPayConfig
	client *http.Client
	clock  func() time.Time
}

// NewDusuPay returns a configured DusuPay provider. A nil httpClient
// falls back to the frame-managed default with a 15s timeout — DusuPay
// occasionally takes 10s+ to create a checkout under load.
func NewDusuPay(cfg DusuPayConfig, httpClient *http.Client) *DusuPay {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &DusuPay{cfg: cfg, client: httpClient, clock: time.Now}
}

// Name identifies the provider in logs and frontend copy.
func (d *DusuPay) Name() string { return "dusupay" }

// dusuPayCheckoutRequest is the JSON body we send to
// POST {BaseURL}/v1/collections.
type dusuPayCheckoutRequest struct {
	Currency          string `json:"currency"`
	Amount            int64  `json:"amount"`            // DusuPay expects base units (shillings, not cents)
	Method            string `json:"method,omitempty"`  // "mobile_money" / "card" / "bank" — blank = let DusuPay choose
	Country           string `json:"country"`
	CustomerEmail     string `json:"customer_email"`
	CustomerReference string `json:"customer_reference"`
	MerchantReference string `json:"merchant_reference"` // echoed back on webhook
	Description       string `json:"description"`
	CallbackURL       string `json:"callback_url"`
	RedirectURL       string `json:"redirect_url"`
	Metadata          map[string]string `json:"metadata"`
}

// dusuPayCheckoutResponse mirrors the provider's success envelope.
type dusuPayCheckoutResponse struct {
	Code int `json:"code"`
	Data struct {
		TransactionReference string `json:"transaction_reference"`
		RedirectURL          string `json:"redirect_url"`
		Status               string `json:"status"`
	} `json:"data"`
	Message string `json:"message"`
}

// CreateCheckout creates a DusuPay collection and returns the
// hosted-checkout URL. The user's ProfileID + PlanID are encoded in
// metadata + merchant_reference so the webhook round-trips them.
func (d *DusuPay) CreateCheckout(ctx context.Context, req CheckoutRequest) (CheckoutResponse, error) {
	if d.cfg.BaseURL == "" || d.cfg.APIKey == "" {
		return CheckoutResponse{}, errors.New("dusupay: not configured")
	}

	amount, currency := req.Plan.PriceFor(req.Country)
	// DusuPay amounts are whole local-currency units (UGX, KES…) for
	// mobile-money flows. Plans store cents, so divide by 100 and
	// round — acceptable because our plan prices are whole-shilling
	// amounts anyway (e.g. 1500 KES not 1537.42).
	apiAmount := amount / 100

	body := dusuPayCheckoutRequest{
		Currency:          currency,
		Amount:            apiAmount,
		Country:           req.Country,
		CustomerEmail:     req.Email,
		CustomerReference: req.ProfileID,
		MerchantReference: buildMerchantReference(req.ProfileID, string(req.Plan.ID)),
		Description:       "Stawi Jobs " + req.Plan.Name,
		CallbackURL:       d.cfg.CallbackURL,
		RedirectURL:       firstNonEmpty(req.SuccessURL, d.cfg.CallbackURL),
		Metadata: map[string]string{
			"profile_id": req.ProfileID,
			"plan_id":    string(req.Plan.ID),
		},
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return CheckoutResponse{}, fmt.Errorf("dusupay: marshal request: %w", err)
	}

	endpoint := strings.TrimRight(d.cfg.BaseURL, "/") + "/v1/collections"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return CheckoutResponse{}, fmt.Errorf("dusupay: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+d.cfg.APIKey)

	resp, err := d.client.Do(httpReq)
	if err != nil {
		return CheckoutResponse{}, fmt.Errorf("dusupay: http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return CheckoutResponse{}, fmt.Errorf("dusupay: read body: %w", err)
	}

	if resp.StatusCode >= 400 {
		util.Log(ctx).WithField("status", resp.StatusCode).
			WithField("body", string(raw)).
			Warn("dusupay: checkout rejected")
		return CheckoutResponse{}, fmt.Errorf("dusupay: http %d", resp.StatusCode)
	}

	var decoded dusuPayCheckoutResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return CheckoutResponse{}, fmt.Errorf("dusupay: decode: %w (body=%s)", err, string(raw))
	}
	if decoded.Data.RedirectURL == "" {
		return CheckoutResponse{}, fmt.Errorf("dusupay: empty redirect_url (message=%s)", decoded.Message)
	}

	return CheckoutResponse{
		RedirectURL: decoded.Data.RedirectURL,
		ProviderRef: decoded.Data.TransactionReference,
		Provider:    d.Name(),
		AmountCents: amount,
		Currency:    currency,
	}, nil
}

// VerifyWebhook checks the DusuPay signature header. DusuPay sends
// sha256(secret, raw_body) in lowercase hex in X-Dusupay-Signature.
//
// Constant-time comparison prevents timing side-channels on the
// verification path.
func (d *DusuPay) VerifyWebhook(req *http.Request, body []byte) error {
	if d.cfg.WebhookSecret == "" {
		// No secret configured — reject. Never silently trust.
		return fmt.Errorf("%w: dusupay webhook secret not configured", ErrSignatureInvalid)
	}
	got := req.Header.Get("X-Dusupay-Signature")
	if got == "" {
		return fmt.Errorf("%w: missing X-Dusupay-Signature header", ErrSignatureInvalid)
	}
	mac := hmac.New(sha256.New, []byte(d.cfg.WebhookSecret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(strings.ToLower(strings.TrimSpace(got))), []byte(expected)) {
		return ErrSignatureInvalid
	}
	return nil
}

// dusuPayWebhookPayload is the subset of the webhook envelope we care
// about. DusuPay sends a rich payload; everything outside this is
// ignored and logged via Raw on the WebhookEvent.
type dusuPayWebhookPayload struct {
	EventID              string `json:"id"`
	Status               string `json:"status"`
	TransactionReference string `json:"transaction_reference"`
	MerchantReference    string `json:"merchant_reference"`
	CustomerReference    string `json:"customer_reference"`
	Metadata             struct {
		ProfileID string `json:"profile_id"`
		PlanID    string `json:"plan_id"`
	} `json:"metadata"`
}

// ParseWebhook converts DusuPay's payload into our normalised event.
// We accept profile/plan from either metadata (preferred) or the
// merchant_reference we built at checkout — whichever the payload
// happens to carry back.
func (d *DusuPay) ParseWebhook(body []byte) (WebhookEvent, error) {
	var p dusuPayWebhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return WebhookEvent{}, fmt.Errorf("dusupay: webhook decode: %w", err)
	}

	profileID := p.Metadata.ProfileID
	if profileID == "" {
		profileID = p.CustomerReference
	}
	planID := p.Metadata.PlanID
	// Fall back to decoding merchant_reference.
	if profileID == "" || planID == "" {
		pid, plan := parseMerchantReference(p.MerchantReference)
		if profileID == "" {
			profileID = pid
		}
		if planID == "" {
			planID = plan
		}
	}

	status, err := mapDusuPayStatus(p.Status)
	if err != nil {
		return WebhookEvent{Raw: body}, err
	}

	return WebhookEvent{
		ProfileID:      profileID,
		PlanID:         PlanID(planID),
		SubscriptionID: p.TransactionReference,
		Status:         status,
		EventID:        p.EventID,
		Raw:            body,
	}, nil
}

// mapDusuPayStatus collapses DusuPay's status strings to our enum.
// Unknown statuses return ErrUnhandledEvent so the webhook handler
// 204s instead of 500-ing.
func mapDusuPayStatus(s string) (EventStatus, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "successful", "completed", "success", "paid":
		return StatusPaid, nil
	case "failed", "declined":
		return StatusFailed, nil
	case "cancelled", "canceled":
		return StatusCancelled, nil
	case "expired":
		return StatusExpired, nil
	case "pending", "initiated":
		return StatusPending, nil
	default:
		return "", fmt.Errorf("%w: dusupay status=%s", ErrUnhandledEvent, s)
	}
}

// buildMerchantReference encodes {profile_id, plan_id} in a single
// opaque string we can round-trip through any provider that gives us
// only one free-text reference field. Format: "pid:plan" — colon-
// separated, kept short because DusuPay caps the field at 64 chars.
func buildMerchantReference(profileID, planID string) string {
	return profileID + ":" + planID
}

// parseMerchantReference is the inverse. Robust to malformed input:
// returns empty strings on anything that doesn't match the format,
// so downstream validation can reject.
func parseMerchantReference(ref string) (profileID, planID string) {
	parts := strings.SplitN(ref, ":", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

// firstNonEmpty returns the first non-empty string. Used to let
// per-request SuccessURL override the config default without
// ternaries at call sites.
func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
