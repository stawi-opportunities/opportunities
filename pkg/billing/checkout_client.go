package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// CheckoutSessionClient creates hosted checkout sessions on pay.stawi.org
// (service-payment apps/checkout). Provider-agnostic: the session page embeds
// card collection and routes to whatever payment method.route is configured.
type CheckoutSessionClient interface {
	CreateSession(ctx context.Context, req CreateHostedSessionRequest) (CreateHostedSessionResult, error)
	GetSession(ctx context.Context, ref string) (HostedSessionStatus, error)
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
}

// HTTPCheckoutClient talks Connect JSON to CheckoutService.
type HTTPCheckoutClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewHTTPCheckoutClient builds a Connect JSON client for CheckoutService.
func NewHTTPCheckoutClient(baseURL string) *HTTPCheckoutClient {
	return &HTTPCheckoutClient{
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
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
	units, nanos := splitDecimalAmount(req.Amount)
	body := map[string]any{
		"name":        req.Name,
		"description": req.Description,
		"orderRef":    req.OrderRef,
		"returnUrl":   req.ReturnURL,
		"amount": map[string]any{
			"currencyCode": req.Currency,
			"units":        units,
			"nanos":        nanos,
		},
		"methods":  req.Methods,
		"metadata": req.Metadata,
	}
	if req.ProfileID != "" || req.DisplayName != "" || req.Phone != "" {
		payer := map[string]any{
			"profileId":   req.ProfileID,
			"displayName": req.DisplayName,
		}
		if req.Phone != "" {
			payer["contacts"] = []map[string]any{
				{"contactId": req.Phone, "msisdn": req.Phone},
			}
		}
		body["payer"] = payer
	}
	// Email is applied via profile prefill on checkout; also stash in metadata.
	if body["metadata"] == nil {
		body["metadata"] = map[string]string{}
	}
	if md, ok := body["metadata"].(map[string]string); ok && req.Email != "" {
		md["email"] = req.Email
	}

	var resp struct {
		Data struct {
			Ref     string `json:"ref"`
			PageUrl string `json:"pageUrl"`
		} `json:"data"`
	}
	if err := c.post(ctx, "/checkout.v1.CheckoutService/CreateCheckoutSession", body, &resp); err != nil {
		return CreateHostedSessionResult{}, err
	}
	if resp.Data.Ref == "" || resp.Data.PageUrl == "" {
		return CreateHostedSessionResult{}, fmt.Errorf("checkout: empty session response")
	}
	return CreateHostedSessionResult{Ref: resp.Data.Ref, PageURL: resp.Data.PageUrl}, nil
}

func (c *HTTPCheckoutClient) GetSession(ctx context.Context, ref string) (HostedSessionStatus, error) {
	var resp struct {
		Data struct {
			Ref      string `json:"ref"`
			Status   string `json:"status"`
			PromptId string `json:"promptId"`
		} `json:"data"`
	}
	// Connect uses numeric enums in proto JSON sometimes; accept string forms.
	body := map[string]any{"ref": ref}
	if err := c.post(ctx, "/checkout.v1.CheckoutService/GetCheckoutSession", body, &resp); err != nil {
		return HostedSessionStatus{}, err
	}
	return HostedSessionStatus{
		Ref:      resp.Data.Ref,
		Status:   normalizeSessionStatus(resp.Data.Status),
		PromptID: resp.Data.PromptId,
	}, nil
}

func (c *HTTPCheckoutClient) post(ctx context.Context, path string, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("checkout marshal: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Connect-Protocol-Version", "1")

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("checkout request: %w", err)
	}
	defer resp.Body.Close()
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

func splitDecimalAmount(amount string) (units int64, nanos int32) {
	amount = strings.TrimSpace(amount)
	if amount == "" {
		return 0, 0
	}
	neg := false
	if strings.HasPrefix(amount, "-") {
		neg = true
		amount = amount[1:]
	}
	parts := strings.SplitN(amount, ".", 2)
	var u int64
	fmt.Sscanf(parts[0], "%d", &u)
	var n int32
	if len(parts) == 2 {
		frac := parts[1]
		if len(frac) > 9 {
			frac = frac[:9]
		}
		for len(frac) < 9 {
			frac += "0"
		}
		var fi int64
		fmt.Sscanf(frac, "%d", &fi)
		n = int32(fi)
	}
	if neg {
		u = -u
		n = -n
	}
	return u, n
}

func normalizeSessionStatus(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case strings.Contains(s, "completed"), s == "2":
		return "completed"
	case strings.Contains(s, "failed"), s == "3":
		return "failed"
	case strings.Contains(s, "processing"), s == "1":
		return "processing"
	case strings.Contains(s, "expired"), s == "4":
		return "expired"
	default:
		return "pending"
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
