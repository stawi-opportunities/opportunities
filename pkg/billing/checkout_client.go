package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CheckoutSessionClient creates hosted checkout sessions on pay.stawi.org
// (service-payment apps/checkout). Provider-agnostic: the session page embeds
// card collection and routes to whatever payment method.route is configured.
type CheckoutSessionClient interface {
	CreateSession(ctx context.Context, req CreateHostedSessionRequest) (CreateHostedSessionResult, error)
	GetSession(ctx context.Context, refOrOrderRef string) (HostedSessionStatus, error)
}

// CreateHostedSessionRequest is the portable session create payload.
type CreateHostedSessionRequest struct {
	Name        string
	Description string
	Amount      string // decimal major units, e.g. "50.00"
	Currency    string
	OrderRef    string
	ReturnURL   string
	ProfileID   string
	DisplayName string
	Email       string
	Phone       string
	// Methods restricts keys on the page; default card-first on checkout service.
	Methods  []string
	Metadata map[string]string
}

// CreateHostedSessionResult is the pay page the SPA should open.
type CreateHostedSessionResult struct {
	Ref     string
	PageURL string
}

// HostedSessionStatus is a subset of checkout session for polling.
type HostedSessionStatus struct {
	Ref      string
	Status   string // pending | processing | completed | failed | expired
	PromptID string
	OrderRef string
}

// HTTPCheckoutClient talks to checkout's cluster-internal session API.
//
// Uses POST /internal/v1/sessions with X-Checkout-Internal-Token rather than
// Connect RPC, because Connect on checkout enforces service_checkout
// permissions that product SAs may not hold yet. The token must match
// CHECKOUT_INTERNAL_TOKEN (or CHECKOUT_SIGNING_SECRET) on the checkout service.
type HTTPCheckoutClient struct {
	BaseURL       string
	InternalToken string
	PublicBaseURL string // fallback page URL builder
	HTTPClient    *http.Client
}

// NewHTTPCheckoutClient builds a client for the internal sessions API.
func NewHTTPCheckoutClient(baseURL, internalToken, publicBaseURL string) *HTTPCheckoutClient {
	return &HTTPCheckoutClient{
		BaseURL:       strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		InternalToken: strings.TrimSpace(internalToken),
		PublicBaseURL: strings.TrimRight(strings.TrimSpace(publicBaseURL), "/"),
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *HTTPCheckoutClient) CreateSession(
	ctx context.Context,
	req CreateHostedSessionRequest,
) (CreateHostedSessionResult, error) {
	if c == nil || c.BaseURL == "" {
		return CreateHostedSessionResult{}, fmt.Errorf("checkout client not configured")
	}
	if c.InternalToken == "" {
		return CreateHostedSessionResult{}, fmt.Errorf(
			"checkout internal token not configured (set CHECKOUT_INTERNAL_TOKEN to match checkout)",
		)
	}

	body := map[string]any{
		"name":         req.Name,
		"description":  req.Description,
		"order_ref":    req.OrderRef,
		"return_url":   req.ReturnURL,
		"amount":       req.Amount,
		"currency":     req.Currency,
		"methods":      req.Methods,
		"metadata":     req.Metadata,
		"profile_id":   req.ProfileID,
		"display_name": req.DisplayName,
		"email":        req.Email,
		"phone":        req.Phone,
	}

	var resp struct {
		Ref     string `json:"ref"`
		PageURL string `json:"page_url"`
		PageUrl string `json:"pageUrl"`
		Error   string `json:"error"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/internal/v1/sessions", body, &resp); err != nil {
		return CreateHostedSessionResult{}, err
	}
	if resp.Error != "" {
		return CreateHostedSessionResult{}, fmt.Errorf("checkout: %s", resp.Error)
	}
	ref := resp.Ref
	pageURL := firstNonEmpty(resp.PageURL, resp.PageUrl)
	if pageURL == "" && c.PublicBaseURL != "" && ref != "" {
		pageURL = c.PublicBaseURL + "/c/" + ref
	}
	if ref == "" || pageURL == "" {
		return CreateHostedSessionResult{}, fmt.Errorf("checkout: empty session response")
	}
	return CreateHostedSessionResult{Ref: ref, PageURL: pageURL}, nil
}

// GetSession loads session status by session ref or product order_ref (chk_*).
// This is how matching detects completed payments and activates subscriptions.
func (c *HTTPCheckoutClient) GetSession(ctx context.Context, refOrOrderRef string) (HostedSessionStatus, error) {
	if c == nil || c.BaseURL == "" {
		return HostedSessionStatus{}, fmt.Errorf("checkout client not configured")
	}
	id := strings.TrimSpace(refOrOrderRef)
	if id == "" {
		return HostedSessionStatus{}, fmt.Errorf("checkout: empty session id")
	}

	var resp struct {
		Ref      string `json:"ref"`
		Status   string `json:"status"`
		PromptID string `json:"prompt_id"`
		OrderRef string `json:"order_ref"`
		Error    string `json:"error"`
	}

	// Prefer order_ref lookup when the id looks like our minted chk_* key.
	if strings.HasPrefix(id, "chk_") {
		path := "/internal/v1/sessions?order_ref=" + url.QueryEscape(id)
		if err := c.doJSON(ctx, http.MethodGet, path, nil, &resp); err == nil && resp.Ref != "" {
			return HostedSessionStatus{
				Ref: resp.Ref, Status: resp.Status, PromptID: resp.PromptID, OrderRef: resp.OrderRef,
			}, nil
		}
	}

	// Session ref path (and fallback if order_ref route is older).
	path := "/internal/v1/sessions/" + url.PathEscape(id)
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &resp); err != nil {
		// Last resort: public status endpoint (works when id is the session ref).
		if st, pubErr := c.publicStatus(ctx, id); pubErr == nil {
			return st, nil
		}
		return HostedSessionStatus{}, err
	}
	if resp.Error != "" {
		return HostedSessionStatus{}, fmt.Errorf("checkout: %s", resp.Error)
	}
	if resp.Ref == "" {
		resp.Ref = id
	}
	return HostedSessionStatus{
		Ref: resp.Ref, Status: resp.Status, PromptID: resp.PromptID, OrderRef: resp.OrderRef,
	}, nil
}

func (c *HTTPCheckoutClient) publicStatus(ctx context.Context, ref string) (HostedSessionStatus, error) {
	path := "/c/" + url.PathEscape(ref) + "/status"
	var resp struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return HostedSessionStatus{}, err
	}
	if resp.Error == "not_found" || resp.Status == "" {
		return HostedSessionStatus{}, fmt.Errorf("checkout: session not found")
	}
	return HostedSessionStatus{Ref: ref, Status: resp.Status}, nil
}

func (c *HTTPCheckoutClient) doJSON(ctx context.Context, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("checkout marshal: %w", err)
		}
		rdr = bytes.NewReader(raw)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	httpReq.Header.Set("Accept", "application/json")
	if c.InternalToken != "" {
		httpReq.Header.Set("X-Checkout-Internal-Token", c.InternalToken)
	}

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("checkout request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("checkout http %d: %s", resp.StatusCode, truncate(string(respBody), 256))
	}
	if out == nil || len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("checkout decode: %w", err)
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
