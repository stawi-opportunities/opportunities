// Package httpx provides HTTP and headless browser fetching.
package httpx

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// BrowserClient renders JavaScript-heavy pages using a headless browser.
// It maintains a shared browser instance and serializes page loads.
type BrowserClient struct {
	mu        sync.Mutex
	browser   *rod.Browser
	timeout   time.Duration
	userAgent string
}

// NewBrowserClient creates a headless Chrome browser.
// The browser process is started lazily on first use.
func NewBrowserClient(timeout time.Duration, userAgent string) *BrowserClient {
	return &BrowserClient{
		timeout:   timeout,
		userAgent: userAgent,
	}
}

func (c *BrowserClient) ensureBrowser() error {
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
		MustLaunch()

	browser := rod.New().ControlURL(u)
	if err := browser.Connect(); err != nil {
		return fmt.Errorf("browser connect: %w", err)
	}
	c.browser = browser
	return nil
}

// Get renders the URL in headless Chrome and returns the fully-rendered HTML.
func (c *BrowserClient) Get(ctx context.Context, url string) ([]byte, int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureBrowser(); err != nil {
		return nil, 0, err
	}

	page, err := c.browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return nil, 0, fmt.Errorf("browser new page: %w", err)
	}
	defer func() { _ = page.Close() }()

	page = page.Timeout(c.timeout)

	if c.userAgent != "" {
		page.MustSetUserAgent(&proto.NetworkSetUserAgentOverride{
			UserAgent: c.userAgent,
		})
	}

	err = page.Navigate(url)
	if err != nil {
		return nil, 0, fmt.Errorf("browser navigate: %w", err)
	}

	// Wait for network idle (page finished loading JS). Timeout isn't
	// fatal — partial pages often still contain enough content for
	// extraction.
	_ = page.WaitStable(2 * time.Second)

	html, err := page.HTML()
	if err != nil {
		return nil, 0, fmt.Errorf("browser get html: %w", err)
	}

	return []byte(html), 200, nil
}

// Close shuts down the browser process.
func (c *BrowserClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.browser != nil {
		_ = c.browser.Close()
		c.browser = nil
	}
}
