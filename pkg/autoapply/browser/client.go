// Package browser provides a go-rod-backed headless browser pool
// purpose-built for ATS form-fill and submission. Unlike the read-only
// BrowserClient in pkg/connectors/httpx, this client exposes
// FillAndSubmit: navigate → fill fields → upload file → click submit
// → verify success.
package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"

	"github.com/stawi-opportunities/opportunities/pkg/telemetry"
)

// ErrCAPTCHA is returned when a CAPTCHA iframe is detected on the page.
// Treated as a terminal skip (no retry).
var ErrCAPTCHA = errors.New("browser: CAPTCHA detected")

// ErrElementNotFound is returned when a required selector is absent
// after the configured timeout. Treated as a terminal skip.
var ErrElementNotFound = errors.New("browser: required element not found")

// ErrSubmitNotConfirmed is returned when the page after submit click
// does not show the expected confirmation signal. Treated as a terminal
// skip — better to under-report than double-submit.
var ErrSubmitNotConfirmed = errors.New("browser: submission not confirmed")

// SubmitOptions controls one FillAndSubmit invocation.
type SubmitOptions struct {
	URL         string
	TextFields  map[string]string
	FileField   string
	FileBytes   []byte
	FileName    string
	SubmitSel   string
	// ConfirmSel is a CSS selector that must appear after submit for the
	// submission to be considered successful. Optional but strongly
	// recommended per-ATS — without it any non-erroring click is a
	// "success" even if the form silently failed validation.
	ConfirmSel string
	// ConfirmText is matched against page body text after submit; if
	// non-empty and ConfirmSel is empty, the body must contain this
	// substring (case-insensitive).
	ConfirmText string
}

// ApplyClient is the public interface used by submitters. It is
// implemented by the *Pool. Kept narrow so tests can substitute a fake.
type ApplyClient interface {
	FillAndSubmit(ctx context.Context, opts SubmitOptions) error
	GetHTML(ctx context.Context, url string) (string, error)
	Close()
}

// Pool maintains a fixed-size set of headless Chromium instances.
// Acquire blocks on a channel so the worker count caps concurrency.
// Each browser has its own mutex so a hung page does not block siblings.
type Pool struct {
	timeout   time.Duration
	userAgent string

	slots     chan *instance
	instances []*instance
	closed    bool
	closeMu   sync.Mutex
}

// instance is a single browser process owned by the pool.
type instance struct {
	mu      sync.Mutex
	browser *rod.Browser
}

// NewPool constructs a browser pool of the given size. Browsers are
// launched lazily on first acquire so a pool with size > 0 does not
// immediately fork N Chromiums.
func NewPool(size int, timeout time.Duration, userAgent string) *Pool {
	if size < 1 {
		size = 1
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if userAgent == "" {
		userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	}
	p := &Pool{
		timeout:   timeout,
		userAgent: userAgent,
		slots:     make(chan *instance, size),
		instances: make([]*instance, size),
	}
	for i := 0; i < size; i++ {
		inst := &instance{}
		p.instances[i] = inst
		p.slots <- inst
	}
	return p
}

// NewApplyClient is a convenience for callers that want the legacy
// single-browser shape. Equivalent to NewPool(1, timeout, userAgent).
func NewApplyClient(timeout time.Duration, userAgent string) ApplyClient {
	return NewPool(1, timeout, userAgent)
}

// acquire pulls an instance from the pool, blocking on ctx.
func (p *Pool) acquire(ctx context.Context) (*instance, error) {
	select {
	case inst := <-p.slots:
		return inst, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *Pool) release(inst *instance) {
	p.closeMu.Lock()
	closed := p.closed
	p.closeMu.Unlock()
	if closed {
		return
	}
	p.slots <- inst
}

// Close shuts down every browser in the pool. Idempotent.
func (p *Pool) Close() {
	p.closeMu.Lock()
	if p.closed {
		p.closeMu.Unlock()
		return
	}
	p.closed = true
	p.closeMu.Unlock()
	for _, inst := range p.instances {
		inst.mu.Lock()
		if inst.browser != nil {
			_ = inst.browser.Close()
			inst.browser = nil
		}
		inst.mu.Unlock()
	}
}

// ensure starts (or restarts) the underlying browser when the existing
// one is missing or unhealthy. Caller holds inst.mu.
func (i *instance) ensure() error {
	if i.browser != nil {
		// Health check: a quick Version() call that does not require a
		// live page. If it fails the process is dead — drop and relaunch.
		if _, err := i.browser.Version(); err == nil {
			return nil
		}
		_ = i.browser.Close()
		i.browser = nil
		telemetry.RecordAutoApplyBrowserRestart("health_check")
	}

	path, _ := launcher.LookPath()
	u, err := launcher.New().
		Bin(path).
		Headless(true).
		Leakless(true).
		Set("disable-gpu").
		Set("no-sandbox").
		Set("disable-dev-shm-usage").
		Set("disable-blink-features", "AutomationControlled").
		Launch()
	if err != nil {
		telemetry.RecordAutoApplyBrowserRestart("launch_failed")
		return fmt.Errorf("browser: launch: %w", err)
	}

	b := rod.New().ControlURL(u)
	if err := b.Connect(); err != nil {
		telemetry.RecordAutoApplyBrowserRestart("connect_failed")
		return fmt.Errorf("browser: connect: %w", err)
	}
	i.browser = b
	return nil
}

// FillAndSubmit navigates to opts.URL, fills text fields, optionally
// uploads a file, clicks submit, then asserts a success signal. ctx
// cancellation interrupts in-flight browser ops.
//
// Non-nil errors that are not ErrCAPTCHA / ErrElementNotFound /
// ErrSubmitNotConfirmed are transient (retry).
func (p *Pool) FillAndSubmit(ctx context.Context, opts SubmitOptions) error {
	inst, err := p.acquire(ctx)
	if err != nil {
		return err
	}
	defer p.release(inst)

	inst.mu.Lock()
	defer inst.mu.Unlock()

	if err := inst.ensure(); err != nil {
		return err
	}

	page, err := inst.browser.Context(ctx).Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		// A failed page-create on an open browser usually means the
		// connection is dead — drop it so the next call relaunches.
		_ = inst.browser.Close()
		inst.browser = nil
		telemetry.RecordAutoApplyBrowserRestart("crashed")
		return fmt.Errorf("browser: new page: %w", err)
	}
	defer func() { _ = page.Close() }()

	page = page.Context(ctx).Timeout(p.timeout)
	if err := page.SetUserAgent(&proto.NetworkSetUserAgentOverride{UserAgent: p.userAgent}); err != nil {
		return fmt.Errorf("browser: set user agent: %w", err)
	}

	if err := page.Navigate(opts.URL); err != nil {
		return fmt.Errorf("browser: navigate: %w", err)
	}
	_ = page.WaitStable(2 * time.Second)

	if hasCAPTCHA(page) {
		telemetry.RecordAutoApplyCaptcha(hostBucket(opts.URL))
		return ErrCAPTCHA
	}

	for sel, val := range opts.TextFields {
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

	if opts.FileField != "" && len(opts.FileBytes) > 0 {
		tmpPath, err := writeTempFile(opts.FileBytes, opts.FileName)
		if err == nil {
			defer func() { _ = os.Remove(tmpPath) }()
			if fileEl, err := page.Timeout(5 * time.Second).Element(opts.FileField); err == nil {
				_ = fileEl.SetFiles([]string{tmpPath})
			}
		}
	}

	submitEl, err := page.Timeout(10 * time.Second).Element(opts.SubmitSel)
	if err != nil {
		return ErrElementNotFound
	}
	if err := submitEl.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return fmt.Errorf("browser: click submit: %w", err)
	}

	_ = page.WaitStable(3 * time.Second)

	if hasCAPTCHA(page) {
		telemetry.RecordAutoApplyCaptcha(hostBucket(opts.URL))
		return ErrCAPTCHA
	}

	if !verifySubmit(page, opts) {
		return ErrSubmitNotConfirmed
	}

	return nil
}

// GetHTML returns the fully-rendered HTML of url. Used by the LLM form
// handler to inspect the apply page before filling.
func (p *Pool) GetHTML(ctx context.Context, url string) (string, error) {
	inst, err := p.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer p.release(inst)

	inst.mu.Lock()
	defer inst.mu.Unlock()

	if err := inst.ensure(); err != nil {
		return "", err
	}

	page, err := inst.browser.Context(ctx).Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		_ = inst.browser.Close()
		inst.browser = nil
		telemetry.RecordAutoApplyBrowserRestart("crashed")
		return "", fmt.Errorf("browser: new page: %w", err)
	}
	defer func() { _ = page.Close() }()

	page = page.Context(ctx).Timeout(p.timeout)
	if err := page.SetUserAgent(&proto.NetworkSetUserAgentOverride{UserAgent: p.userAgent}); err != nil {
		return "", fmt.Errorf("browser: set user agent: %w", err)
	}

	if err := page.Navigate(url); err != nil {
		return "", fmt.Errorf("browser: navigate: %w", err)
	}
	_ = page.WaitStable(2 * time.Second)

	return page.HTML()
}

// verifySubmit checks the post-submit page for one of the configured
// confirmation signals. Returns true when the page passes.
//
// When neither ConfirmSel nor ConfirmText is set the function returns
// true so the legacy "trust the click" behaviour is preserved for
// submitters that have not yet declared a confirmation strategy. New
// submitters should always set one.
func verifySubmit(page *rod.Page, opts SubmitOptions) bool {
	if opts.ConfirmSel == "" && opts.ConfirmText == "" {
		return true
	}
	if opts.ConfirmSel != "" {
		if _, err := page.Timeout(8 * time.Second).Element(opts.ConfirmSel); err == nil {
			return true
		}
	}
	if opts.ConfirmText != "" {
		html, err := page.HTML()
		if err == nil && strings.Contains(strings.ToLower(html), strings.ToLower(opts.ConfirmText)) {
			return true
		}
	}
	return false
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

// hostBucket returns the apex domain (or "unknown") for telemetry
// labels — keeps cardinality bounded vs raw URLs.
func hostBucket(rawURL string) string {
	lower := strings.ToLower(rawURL)
	for _, host := range []string{
		"boards.greenhouse.io", "greenhouse.io",
		"jobs.lever.co", "lever.co",
		"myworkdayjobs.com", "myworkday.com",
		"smartrecruiters.com",
	} {
		if strings.Contains(lower, host) {
			return host
		}
	}
	return "other"
}

// writeTempFile writes data into a freshly-created temp file. The name
// is sanitised before being embedded in the temp file pattern so a
// caller-supplied filename containing path separators cannot escape
// the temp directory.
func writeTempFile(data []byte, name string) (string, error) {
	safe := safeBaseName(name)
	f, err := os.CreateTemp("", "autoapply_*_"+safe)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return "", err
	}
	return f.Name(), nil
}

// safeBaseName strips path separators (POSIX and Windows-style) and
// limits the filename to a safe character set + length. Used for
// temp-file pattern interpolation.
func safeBaseName(name string) string {
	// Treat both / and \ as separators so a Content-Disposition crafted
	// on Windows can't smuggle a path through filepath.Base on Linux.
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	if name == "." || name == "/" || name == "" {
		return "resume.pdf"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
		}
		if b.Len() >= 64 {
			break
		}
	}
	if b.Len() == 0 {
		return "resume.pdf"
	}
	return b.String()
}
