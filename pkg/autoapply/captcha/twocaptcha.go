// Package captcha provides CaptchaSolver implementations for the browser
// auto-apply pool. TwoCaptcha delegates challenges (reCAPTCHA v2 and v2
// enterprise, hCaptcha, Cloudflare Turnstile) to 2captcha.com via its
// JSON API (createTask / getTaskResult) and returns the response token
// for the browser to inject into the form.
package captcha

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/autoapply/browser"
)

const (
	defaultBaseURL      = "https://api.2captcha.com"
	defaultPollInterval = 5 * time.Second
	defaultSolveTimeout = 180 * time.Second
)

// TwoCaptcha implements browser.CaptchaSolver against 2captcha.com.
type TwoCaptcha struct {
	apiKey  string
	baseURL string
	http    *http.Client
	poll    time.Duration
	timeout time.Duration
}

// NewTwoCaptcha constructs a solver for the given API key.
func NewTwoCaptcha(apiKey string) *TwoCaptcha {
	return &TwoCaptcha{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		http:    &http.Client{Timeout: 30 * time.Second},
		poll:    defaultPollInterval,
		timeout: defaultSolveTimeout,
	}
}

// WithTimeout bounds a single end-to-end solve (create + poll). Returns
// the receiver for chaining.
func (c *TwoCaptcha) WithTimeout(d time.Duration) *TwoCaptcha {
	if d > 0 {
		c.timeout = d
	}
	return c
}

// Solve implements browser.CaptchaSolver.
func (c *TwoCaptcha) Solve(ctx context.Context, task browser.CaptchaTask) (string, error) {
	if c.apiKey == "" {
		return "", fmt.Errorf("2captcha: missing API key")
	}
	taskID, err := c.createTask(ctx, task)
	if err != nil {
		return "", err
	}
	return c.pollResult(ctx, taskID)
}

// createTask submits the challenge and returns the 2captcha task id.
func (c *TwoCaptcha) createTask(ctx context.Context, t browser.CaptchaTask) (json.Number, error) {
	spec := map[string]any{
		"websiteURL": t.PageURL,
		"websiteKey": t.SiteKey,
	}
	switch t.Type {
	case browser.CaptchaHCaptcha:
		spec["type"] = "HCaptchaTaskProxyless"
		if t.Data != "" {
			spec["data"] = t.Data
		}
	case browser.CaptchaTurnstile:
		spec["type"] = "TurnstileTaskProxyless"
		if t.Action != "" {
			spec["action"] = t.Action
		}
		if t.Data != "" {
			spec["data"] = t.Data
		}
	case browser.CaptchaRecaptchaV2Enterprise:
		spec["type"] = "RecaptchaV2EnterpriseTaskProxyless"
		if t.Invisible {
			spec["isInvisible"] = true
		}
		payload := map[string]any{}
		if t.Data != "" {
			payload["s"] = t.Data
		}
		if t.Action != "" {
			spec["pageAction"] = t.Action
		}
		if len(payload) > 0 {
			spec["enterprisePayload"] = payload
		}
	default: // reCAPTCHA v2
		spec["type"] = "RecaptchaV2TaskProxyless"
		if t.Invisible {
			spec["isInvisible"] = true
		}
	}

	var out struct {
		ErrorID          int         `json:"errorId"`
		ErrorCode        string      `json:"errorCode"`
		ErrorDescription string      `json:"errorDescription"`
		TaskID           json.Number `json:"taskId"`
	}
	if err := c.post(ctx, "/createTask", map[string]any{
		"clientKey": c.apiKey,
		"task":      spec,
	}, &out); err != nil {
		return "", err
	}
	if out.ErrorID != 0 {
		return "", fmt.Errorf("2captcha createTask: %s: %s", out.ErrorCode, out.ErrorDescription)
	}
	return out.TaskID, nil
}

// pollResult polls getTaskResult until the solution is ready or the
// solve timeout / ctx elapses.
func (c *TwoCaptcha) pollResult(ctx context.Context, taskID json.Number) (string, error) {
	deadline := time.Now().Add(c.timeout)
	ticker := time.NewTicker(c.poll)
	defer ticker.Stop()

	for {
		var out struct {
			ErrorID          int    `json:"errorId"`
			ErrorCode        string `json:"errorCode"`
			ErrorDescription string `json:"errorDescription"`
			Status           string `json:"status"`
			Solution         struct {
				GRecaptchaResponse string `json:"gRecaptchaResponse"`
				Token              string `json:"token"`
			} `json:"solution"`
		}
		if err := c.post(ctx, "/getTaskResult", map[string]any{
			"clientKey": c.apiKey,
			"taskId":    taskID,
		}, &out); err != nil {
			return "", err
		}
		if out.ErrorID != 0 {
			return "", fmt.Errorf("2captcha getTaskResult: %s: %s", out.ErrorCode, out.ErrorDescription)
		}
		if out.Status == "ready" {
			if out.Solution.GRecaptchaResponse != "" {
				return out.Solution.GRecaptchaResponse, nil
			}
			return out.Solution.Token, nil
		}

		if time.Now().After(deadline) {
			return "", fmt.Errorf("2captcha: solve timed out after %s", c.timeout)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

// post issues a JSON POST to the 2captcha API and decodes the response.
func (c *TwoCaptcha) post(ctx context.Context, path string, body any, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("2captcha %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("2captcha %s: http %d: %s", path, resp.StatusCode, string(b))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Ensure TwoCaptcha satisfies the solver interface.
var _ browser.CaptchaSolver = (*TwoCaptcha)(nil)
