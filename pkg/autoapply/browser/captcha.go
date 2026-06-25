package browser

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"

	"github.com/stawi-opportunities/opportunities/pkg/telemetry"
)

// CaptchaType enumerates the challenge kinds a solver can handle.
type CaptchaType string

const (
	CaptchaRecaptchaV2           CaptchaType = "recaptcha_v2"
	CaptchaRecaptchaV2Enterprise CaptchaType = "recaptcha_v2_enterprise"
	CaptchaHCaptcha              CaptchaType = "hcaptcha"
	CaptchaTurnstile             CaptchaType = "turnstile"
)

// CaptchaTask describes a detected challenge handed to a CaptchaSolver.
type CaptchaTask struct {
	Type      CaptchaType
	SiteKey   string
	PageURL   string
	Action    string // reCAPTCHA enterprise / v3 action, when present
	Data      string // hCaptcha "data" / enterprise "s" payload, when present
	Invisible bool
}

// CaptchaSolver returns a response token for a detected challenge (e.g.
// via 2captcha). The token is injected into the page so the form accepts
// the submission. Implementations live outside this package and are wired
// in via Pool.WithCaptchaSolver.
type CaptchaSolver interface {
	Solve(ctx context.Context, task CaptchaTask) (token string, err error)
}

// WithCaptchaSolver attaches a solver so detected CAPTCHAs are solved
// instead of skipped. A nil solver keeps the historical skip behaviour.
// Returns the receiver for chaining.
func (p *Pool) WithCaptchaSolver(s CaptchaSolver) *Pool {
	p.solver = s
	return p
}

// handleCaptcha is the single decision point for a detected CAPTCHA.
// The bool reports whether a token was solved-and-injected (so the caller
// can resubmit when the challenge gated a submit click):
//   - no challenge          → (false, nil) — carry on
//   - challenge, no solver  → (false, ErrCAPTCHA) — skip, as before
//   - challenge, solved     → (true, nil)
//   - challenge, solve fail  → (false, ErrCAPTCHA)
func (p *Pool) handleCaptcha(ctx context.Context, page *rod.Page, pageURL string) (bool, error) {
	if !captchaPending(page) {
		return false, nil
	}
	host := hostBucket(pageURL)
	telemetry.RecordAutoApplyCaptcha(host)

	if p.solver == nil {
		return false, ErrCAPTCHA
	}

	task, ok := detectCaptcha(page, pageURL)
	if !ok {
		// Marker present but no sitekey to solve against.
		telemetry.RecordAutoApplyCaptchaSolve(host, "unknown", "failed")
		return false, ErrCAPTCHA
	}

	token, err := p.solver.Solve(ctx, task)
	if err != nil || token == "" {
		telemetry.RecordAutoApplyCaptchaSolve(host, string(task.Type), "failed")
		return false, ErrCAPTCHA
	}
	if err := injectCaptchaToken(page, task.Type, token); err != nil {
		telemetry.RecordAutoApplyCaptchaSolve(host, string(task.Type), "failed")
		return false, ErrCAPTCHA
	}

	telemetry.RecordAutoApplyCaptchaSolve(host, string(task.Type), "solved")
	settle(page)
	return true, nil
}

// captchaPending reports whether the page is currently blocked on an
// UNSOLVED challenge: a known widget is present and its response token is
// still empty.
//
// This is the real "triggered" signal, but it is only meaningful AFTER a
// submit attempt: on page load an invisible/enterprise widget is also
// present with an empty token, so calling this pre-submit would treat
// every form as challenged. Post-submit, an auto-passing invisible widget
// has populated its token (→ not pending), while a widget that genuinely
// demanded a challenge leaves it empty (→ pending).
func captchaPending(page *rod.Page) bool {
	res, err := page.Eval(`() => {
		const widget = document.querySelector('.g-recaptcha, .h-captcha, .cf-turnstile');
		if (!widget) return false;
		const resp = document.querySelector('[name="g-recaptcha-response"], [name="h-captcha-response"], [name="cf-turnstile-response"]');
		return !resp || !resp.value;
	}`)
	if err != nil {
		return false
	}
	return res.Value.Bool()
}

// detectCaptcha inspects the live DOM for a supported widget and returns
// the task to solve. ok is false when no extractable challenge is found.
func detectCaptcha(page *rod.Page, pageURL string) (CaptchaTask, bool) {
	if t, ok := readWidget(page, ".h-captcha", CaptchaHCaptcha, pageURL); ok {
		return t, true
	}
	if t, ok := readWidget(page, ".cf-turnstile", CaptchaTurnstile, pageURL); ok {
		return t, true
	}
	if t, ok := readWidget(page, ".g-recaptcha", CaptchaRecaptchaV2, pageURL); ok {
		if isEnterpriseRecaptcha(page) {
			t.Type = CaptchaRecaptchaV2Enterprise
		}
		return t, true
	}
	return CaptchaTask{}, false
}

// readWidget reads a captcha widget's data-* attributes into a task.
func readWidget(page *rod.Page, sel string, typ CaptchaType, pageURL string) (CaptchaTask, bool) {
	el, err := page.Timeout(2 * time.Second).Element(sel)
	if err != nil {
		return CaptchaTask{}, false
	}
	key := attr(el, "data-sitekey")
	if key == "" {
		return CaptchaTask{}, false
	}
	return CaptchaTask{
		Type:      typ,
		SiteKey:   key,
		PageURL:   pageURL,
		Action:    attr(el, "data-action"),
		Data:      attr(el, "data-s"),
		Invisible: strings.EqualFold(attr(el, "data-size"), "invisible"),
	}, true
}

func attr(el *rod.Element, name string) string {
	v, err := el.Attribute(name)
	if err != nil || v == nil {
		return ""
	}
	return *v
}

// isEnterpriseRecaptcha reports whether the page loaded the enterprise
// reCAPTCHA build (grecaptcha.enterprise), which 2captcha treats as a
// distinct task type.
func isEnterpriseRecaptcha(page *rod.Page) bool {
	res, err := page.Eval(`() => !!(window.grecaptcha && window.grecaptcha.enterprise)`)
	if err != nil {
		return false
	}
	return res.Value.Bool()
}

// injectCaptchaToken writes the solver token into the page's hidden
// response field(s) and fires a change event so the form picks it up on
// submit. For invisible/enterprise widgets that gate on a JS callback,
// setting the response field is usually sufficient for server-side
// verification, which reads the token from the form post.
func injectCaptchaToken(page *rod.Page, typ CaptchaType, token string) error {
	var selectors string
	switch typ {
	case CaptchaHCaptcha:
		selectors = `[name="h-captcha-response"], [name="g-recaptcha-response"]`
	case CaptchaTurnstile:
		selectors = `[name="cf-turnstile-response"]`
	default: // reCAPTCHA v2 / enterprise
		selectors = `#g-recaptcha-response, [name="g-recaptcha-response"]`
	}
	js := fmt.Sprintf(`() => {
		const els = document.querySelectorAll(%q);
		els.forEach(el => { el.value = %q; el.dispatchEvent(new Event('change', {bubbles:true})); });
		return els.length;
	}`, selectors, token)
	_, err := page.Eval(js)
	return err
}
