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

// PlarConfig holds the Plar.sh adapter settings. Same shape as
// DusuPayConfig for symmetry — base URL, API key for outbound calls,
// webhook secret for inbound signature verification.
type PlarConfig struct {
	BaseURL       string
	APIKey        string
	WebhookSecret string
	CallbackURL   string
	// MerchantID identifies this stawi.jobs account in Plar.
	MerchantID string
}

// Plar implements Provider against Plar.sh's hosted-checkout API.
type Plar struct {
	cfg    PlarConfig
	client *http.Client
	clock  func() time.Time
}

// NewPlar returns a configured Plar provider. Same default-timeout
// pattern as DusuPay.
func NewPlar(cfg PlarConfig, httpClient *http.Client) *Plar {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Plar{cfg: cfg, client: httpClient, clock: time.Now}
}

// Name identifies the provider in logs and frontend copy.
func (p *Plar) Name() string { return "plar" }

// plarCheckoutRequest is what we POST to Plar's hosted-checkout API.
// Amounts are in cents (matching the Catalog). Currency is always
// USD on this adapter — Plar handles the user's local FX on the
// hosted page.
type plarCheckoutRequest struct {
	Amount      int64             `json:"amount"`
	Currency    string            `json:"currency"`
	ProductName string            `json:"product_name"`
	Description string            `json:"description"`
	SuccessURL  string            `json:"success_url"`
	CancelURL   string            `json:"cancel_url"`
	WebhookURL  string            `json:"webhook_url,omitempty"`
	Customer    plarCustomer      `json:"customer"`
	Metadata    map[string]string `json:"metadata"`
	// Interval lets Plar create a recurring-charge session rather
	// than a one-off. Empty => one-off.
	Interval string `json:"interval,omitempty"`
}

type plarCustomer struct {
	Email      string `json:"email,omitempty"`
	ExternalID string `json:"external_id"`
}

type plarCheckoutResponse struct {
	ID          string `json:"id"`
	CheckoutURL string `json:"checkout_url"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
}

// CreateCheckout creates a Plar hosted-checkout session. All non-Africa
// users bill in USD.
func (p *Plar) CreateCheckout(ctx context.Context, req CheckoutRequest) (CheckoutResponse, error) {
	if p.cfg.BaseURL == "" || p.cfg.APIKey == "" {
		return CheckoutResponse{}, errors.New("plar: not configured")
	}

	// Plar bills in the catalog's USD price. PriceFor would return
	// USD for non-African countries anyway, but call it explicitly
	// so local-dev with ?country=KE still generates a USD Plar
	// session when the router sends it here in tests.
	amount := req.Plan.USDCents

	body := plarCheckoutRequest{
		Amount:      amount,
		Currency:    "USD",
		ProductName: "Stawi Jobs " + req.Plan.Name,
		Description: req.Plan.Description,
		SuccessURL:  firstNonEmpty(req.SuccessURL, p.cfg.CallbackURL),
		CancelURL:   firstNonEmpty(req.CancelURL, p.cfg.CallbackURL),
		WebhookURL:  p.cfg.CallbackURL,
		Customer: plarCustomer{
			Email:      req.Email,
			ExternalID: req.ProfileID,
		},
		Metadata: map[string]string{
			"profile_id": req.ProfileID,
			"plan_id":    string(req.Plan.ID),
			"merchant":   p.cfg.MerchantID,
		},
		Interval: req.Plan.Interval, // "monthly" → recurring
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return CheckoutResponse{}, fmt.Errorf("plar: marshal request: %w", err)
	}

	endpoint := strings.TrimRight(p.cfg.BaseURL, "/") + "/v1/checkouts"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return CheckoutResponse{}, fmt.Errorf("plar: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return CheckoutResponse{}, fmt.Errorf("plar: http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return CheckoutResponse{}, fmt.Errorf("plar: read body: %w", err)
	}

	if resp.StatusCode >= 400 {
		util.Log(ctx).WithField("status", resp.StatusCode).
			WithField("body", string(raw)).
			Warn("plar: checkout rejected")
		return CheckoutResponse{}, fmt.Errorf("plar: http %d", resp.StatusCode)
	}

	var decoded plarCheckoutResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return CheckoutResponse{}, fmt.Errorf("plar: decode: %w (body=%s)", err, string(raw))
	}
	if decoded.CheckoutURL == "" {
		return CheckoutResponse{}, fmt.Errorf("plar: empty checkout_url (error=%s)", decoded.Error)
	}

	return CheckoutResponse{
		RedirectURL: decoded.CheckoutURL,
		ProviderRef: decoded.ID,
		Provider:    p.Name(),
		AmountCents: amount,
		Currency:    "USD",
	}, nil
}

// VerifyWebhook verifies Plar's X-Plar-Signature header. Same HMAC-
// SHA256 scheme as DusuPay — the shared helper compareHMAC would
// DRY this up, but keeping per-provider verification explicit
// protects against header-name regressions when one provider changes.
func (p *Plar) VerifyWebhook(req *http.Request, body []byte) error {
	if p.cfg.WebhookSecret == "" {
		return fmt.Errorf("%w: plar webhook secret not configured", ErrSignatureInvalid)
	}
	got := req.Header.Get("X-Plar-Signature")
	if got == "" {
		return fmt.Errorf("%w: missing X-Plar-Signature header", ErrSignatureInvalid)
	}
	mac := hmac.New(sha256.New, []byte(p.cfg.WebhookSecret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(strings.ToLower(strings.TrimSpace(got))), []byte(expected)) {
		return ErrSignatureInvalid
	}
	return nil
}

// plarWebhookPayload is the Plar event envelope.
type plarWebhookPayload struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Status string `json:"status"`
	Data   struct {
		CheckoutID     string `json:"checkout_id"`
		SubscriptionID string `json:"subscription_id"`
		CustomerID     string `json:"customer_id"`
		ExternalID     string `json:"external_id"`
		Metadata       struct {
			ProfileID string `json:"profile_id"`
			PlanID    string `json:"plan_id"`
		} `json:"metadata"`
	} `json:"data"`
}

// ParseWebhook normalises Plar's event stream.
func (p *Plar) ParseWebhook(body []byte) (WebhookEvent, error) {
	var pl plarWebhookPayload
	if err := json.Unmarshal(body, &pl); err != nil {
		return WebhookEvent{}, fmt.Errorf("plar: webhook decode: %w", err)
	}

	status, err := mapPlarEvent(pl.Type, pl.Status)
	if err != nil {
		return WebhookEvent{Raw: body}, err
	}

	profileID := pl.Data.Metadata.ProfileID
	if profileID == "" {
		profileID = pl.Data.ExternalID
	}

	subscriptionID := pl.Data.SubscriptionID
	if subscriptionID == "" {
		subscriptionID = pl.Data.CheckoutID
	}

	return WebhookEvent{
		ProfileID:      profileID,
		PlanID:         PlanID(pl.Data.Metadata.PlanID),
		SubscriptionID: subscriptionID,
		Status:         status,
		EventID:        pl.ID,
		Raw:            body,
	}, nil
}

// mapPlarEvent maps the (type, status) tuple Plar emits to our enum.
// Plar's main events of interest: checkout.completed (first payment),
// subscription.created, subscription.cancelled, charge.failed.
func mapPlarEvent(eventType, status string) (EventStatus, error) {
	t := strings.ToLower(strings.TrimSpace(eventType))
	s := strings.ToLower(strings.TrimSpace(status))
	switch t {
	case "checkout.completed", "subscription.created", "subscription.active", "charge.succeeded":
		return StatusPaid, nil
	case "subscription.cancelled", "subscription.canceled":
		return StatusCancelled, nil
	case "subscription.expired":
		return StatusExpired, nil
	case "charge.failed":
		return StatusFailed, nil
	case "subscription.trialing":
		return StatusTrial, nil
	}
	// Fall back to status-only mapping in case Plar ever ships a
	// status-only legacy webhook.
	switch s {
	case "completed", "succeeded", "active", "paid":
		return StatusPaid, nil
	case "cancelled", "canceled":
		return StatusCancelled, nil
	case "failed":
		return StatusFailed, nil
	}
	return "", fmt.Errorf("%w: plar type=%s status=%s", ErrUnhandledEvent, eventType, status)
}
