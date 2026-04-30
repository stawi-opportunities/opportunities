// Package browser provides a go-rod-backed headless browser client
// purpose-built for form-fill and submission. Unlike the read-only
// BrowserClient in pkg/connectors/httpx, this client exposes
// FillAndSubmit: navigate → fill fields → upload file → click submit.
package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// ErrCAPTCHA is returned when a CAPTCHA iframe is detected on the page.
// Treated as a terminal skip (no retry).
var ErrCAPTCHA = errors.New("browser: CAPTCHA detected")

// ErrElementNotFound is returned when a required selector is absent after
// the configured timeout. Treated as a terminal skip.
var ErrElementNotFound = errors.New("browser: required element not found")

// ApplyClient manages a single headless Chrome instance and serialises
// page interactions. One submission at a time per worker is intentional.
type ApplyClient struct {
	mu        sync.Mutex
	browser   *rod.Browser
	timeout   time.Duration
	userAgent string
}

// NewApplyClient constructs an ApplyClient. The browser is started lazily.
func NewApplyClient(timeout time.Duration, userAgent string) *ApplyClient {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if userAgent == "" {
		userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	}
	return &ApplyClient{timeout: timeout, userAgent: userAgent}
}

func (c *ApplyClient) ensureBrowser() error {
	if c.browser != nil {
		return nil
	}
	path, _ := launcher.LookPath()
	u := launcher.New().
		Bin(path).
		Headless(true).
		Set("disable-gpu").
		Set("no-sandbox").
		Set("disable-dev-shm-usage").
		Set("disable-blink-features", "AutomationControlled").
		MustLaunch()

	b := rod.New().ControlURL(u)
	if err := b.Connect(); err != nil {
		return fmt.Errorf("browser: connect: %w", err)
	}
	c.browser = b
	return nil
}

// FillAndSubmit navigates to url, fills text fields by CSS selector,
// uploads a file to fileField (when fileBytes is non-nil), then clicks
// submitSel.
//
// Non-nil errors are transient (retry). ErrCAPTCHA and ErrElementNotFound
// are terminal skip signals — callers should not retry them.
func (c *ApplyClient) FillAndSubmit(
	_ context.Context,
	url string,
	textFields map[string]string,
	fileField string,
	fileBytes []byte,
	fileName string,
	submitSel string,
) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureBrowser(); err != nil {
		return err
	}

	page, err := c.browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return fmt.Errorf("browser: new page: %w", err)
	}
	defer func() { _ = page.Close() }()

	page = page.Timeout(c.timeout)
	page.MustSetUserAgent(&proto.NetworkSetUserAgentOverride{UserAgent: c.userAgent})

	if err := page.Navigate(url); err != nil {
		return fmt.Errorf("browser: navigate: %w", err)
	}
	_ = page.WaitStable(2 * time.Second)

	if hasCAPTCHA(page) {
		return ErrCAPTCHA
	}

	for sel, val := range textFields {
		if val == "" {
			continue
		}
		el, err := page.Timeout(5 * time.Second).Element(sel)
		if err != nil {
			continue // non-required field; skip silently
		}
		_ = el.SelectAllText()
		_ = el.Input(val)
	}

	if fileField != "" && len(fileBytes) > 0 {
		tmpPath, err := writeTempFile(fileBytes, fileName)
		if err == nil {
			defer func() { _ = os.Remove(tmpPath) }()
			if fileEl, err := page.Timeout(5 * time.Second).Element(fileField); err == nil {
				_ = fileEl.SetFiles([]string{tmpPath})
			}
		}
	}

	submitEl, err := page.Timeout(10 * time.Second).Element(submitSel)
	if err != nil {
		return ErrElementNotFound
	}
	if err := submitEl.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return fmt.Errorf("browser: click submit: %w", err)
	}

	_ = page.WaitStable(3 * time.Second)

	if hasCAPTCHA(page) {
		return ErrCAPTCHA
	}

	return nil
}

// GetHTML returns the fully-rendered HTML of url. Used by the LLM form
// handler to inspect the apply page before filling.
func (c *ApplyClient) GetHTML(_ context.Context, url string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureBrowser(); err != nil {
		return "", err
	}

	page, err := c.browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return "", fmt.Errorf("browser: new page: %w", err)
	}
	defer func() { _ = page.Close() }()

	page = page.Timeout(c.timeout)
	page.MustSetUserAgent(&proto.NetworkSetUserAgentOverride{UserAgent: c.userAgent})

	if err := page.Navigate(url); err != nil {
		return "", fmt.Errorf("browser: navigate: %w", err)
	}
	_ = page.WaitStable(2 * time.Second)

	return page.HTML()
}

// Close shuts down the browser process.
func (c *ApplyClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.browser != nil {
		_ = c.browser.Close()
		c.browser = nil
	}
}

func hasCAPTCHA(page *rod.Page) bool {
	html, err := page.HTML()
	if err != nil {
		return false
	}
	lower := strings.ToLower(html)
	return strings.Contains(lower, "hcaptcha") ||
		strings.Contains(lower, "recaptcha") ||
		strings.Contains(lower, "cf-turnstile")
}

func writeTempFile(data []byte, name string) (string, error) {
	f, err := os.CreateTemp("", "autoapply_*_"+name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return "", err
	}
	return f.Name(), nil
}
