// Package browser provides a go-rod-backed headless browser pool
// purpose-built for ATS form-fill and submission. Unlike the read-only
// BrowserClient in pkg/connectors/httpx, this client exposes
// FillAndSubmit: navigate → fill fields → upload file → click submit
// → verify success.
package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
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
	URL        string
	TextFields map[string]string
	FileField  string
	FileBytes  []byte
	FileName   string
	SubmitSel  string
	// ConfirmSel is a CSS selector that must appear after submit for the
	// submission to be considered successful. Optional but strongly
	// recommended per-ATS — without it any non-erroring click is a
	// "success" even if the form silently failed validation.
	ConfirmSel string
	// ConfirmText is matched against page body text after submit; if
	// non-empty and ConfirmSel is empty, the body must contain this
	// substring (case-insensitive).
	ConfirmText string

	// ScreenshotPath, when set, saves a full-page PNG of the filled form
	// (after fill + upload, before submit). Used for safe live testing.
	ScreenshotPath string
	// NoSubmit fills the form (and screenshots when ScreenshotPath is set)
	// but returns before clicking submit — no submission, CAPTCHA, or
	// confirmation. Used to validate field-filling against a live board
	// without sending a real application.
	NoSubmit bool
	// PostSubmitShotPath, when set, saves a full-page PNG immediately after
	// the submit click — capturing whatever gated the submit (CAPTCHA, the
	// emailed security-code field, a validation error, or success). For
	// observability/debugging.
	PostSubmitShotPath string
}

// FieldDescriptor is a compact, structured view of one fillable form
// control: its real CSS selector plus the label/question text, type, and
// (for selects) the option texts. Feeding these to the LLM — instead of
// raw truncated HTML — keeps the selectors real and the prompt small.
type FieldDescriptor struct {
	Selector string   `json:"selector"`
	Label    string   `json:"label"`
	Type     string   `json:"type"` // text, email, tel, textarea, select, ...
	Required bool     `json:"required"`
	Options  []string `json:"options,omitempty"`
}

// ApplyClient is the public interface used by submitters. It is
// implemented by the *Pool. Kept narrow so tests can substitute a fake.
type ApplyClient interface {
	FillAndSubmit(ctx context.Context, opts SubmitOptions) error
	GetHTML(ctx context.Context, url string) (string, error)
	// GetFormFields navigates to url and returns the live form's fillable
	// controls with real selectors + labels, for LLM-assisted answering.
	GetFormFields(ctx context.Context, url string) ([]FieldDescriptor, error)
	Close()
}

// Pool maintains a fixed-size set of headless Chromium instances.
// Acquire blocks on a channel so the worker count caps concurrency.
// Each browser has its own mutex so a hung page does not block siblings.
type Pool struct {
	timeout   time.Duration
	userAgent string
	solver    CaptchaSolver // nil → CAPTCHAs are skipped (ErrCAPTCHA)

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
	settle(page)

	// No CAPTCHA handling here: on page load an invisible/enterprise
	// widget is present with an empty token regardless of whether a
	// challenge will actually be demanded, so solving now would burn a
	// paid solve on every form. CAPTCHAs are handled reactively after the
	// submit attempt below, where an unsolved challenge is a real signal.

	for sel, val := range opts.TextFields {
		if val == "" {
			continue
		}
		fillField(page, sel, val)
	}

	if opts.FileField != "" && len(opts.FileBytes) > 0 {
		tmpPath, err := writeTempFile(opts.FileBytes, opts.FileName)
		if err == nil {
			defer func() { _ = os.Remove(tmpPath) }()
			if fileEl, err := page.Timeout(5 * time.Second).Element(opts.FileField); err == nil {
				_ = fileEl.SetFiles([]string{tmpPath})
				settle(page) // let the async upload render before submit
			}
		}
	}

	if opts.ScreenshotPath != "" {
		captureForm(page, opts.ScreenshotPath)
	}
	if opts.NoSubmit {
		// Fill-only validation: never click submit. Confirm the submit
		// control exists so the caller knows the form is complete.
		if _, err := page.Timeout(10 * time.Second).Element(opts.SubmitSel); err != nil {
			return ErrElementNotFound
		}
		return nil
	}

	submitEl, err := page.Timeout(10 * time.Second).Element(opts.SubmitSel)
	if err != nil {
		return ErrElementNotFound
	}
	if err := submitEl.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return fmt.Errorf("browser: click submit: %w", err)
	}

	settle(page)

	// Capture the post-submit state when requested — shows what gated the
	// submit (CAPTCHA, the emailed security-code field, a validation error,
	// or success). The relevant UI appears around the submit button, so we
	// wait for processing to settle, scroll there, and shoot the viewport.
	// Debug/observability only.
	if opts.PostSubmitShotPath != "" {
		time.Sleep(3 * time.Second)
		if el, e := page.Timeout(5 * time.Second).Element(opts.SubmitSel); e == nil {
			_ = el.ScrollIntoView()
		}
		if data, e := page.Screenshot(false, nil); e == nil {
			_ = os.WriteFile(opts.PostSubmitShotPath, data, 0o644)
		}
		// Also dump the post-submit fillable fields (e.g. the security-code
		// boxes) so we can see their real selectors.
		if res, e := page.Eval(fieldExtractJS); e == nil {
			_ = os.WriteFile(opts.PostSubmitShotPath+".fields.json", []byte(res.Value.Str()), 0o644)
		}
	}

	// A challenge that gated the submit click (commonly enterprise
	// reCAPTCHA): solve it, then resubmit with the injected token.
	if solved, err := p.handleCaptcha(ctx, page, opts.URL); err != nil {
		return err
	} else if solved {
		if resubmitEl, e := page.Timeout(10 * time.Second).Element(opts.SubmitSel); e == nil {
			_ = resubmitEl.Click(proto.InputMouseButtonLeft, 1)
			settle(page)
		}
	}

	// Verify on a page handle with a FRESH deadline: a long-running gate
	// (e.g. a CAPTCHA solve) can run well past p.timeout, expiring the
	// original handle's context and making verifySubmit's page.HTML() fail
	// — a false "not confirmed" even on a real success (observed on
	// Cloudflare's "Thank you for applying!").
	if !verifySubmit(page.CancelTimeout().Timeout(15*time.Second), opts) {
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
	settle(page)

	return page.HTML()
}

// fieldExtractJS walks the rendered DOM and returns each fillable control
// with a real selector (#id preferred, else [name=...]), its label/question
// text, type, required flag, and (for selects) option texts. Hidden,
// submit, button, file, and search inputs are skipped.
const fieldExtractJS = `() => {
	const out = [];
	const seen = new Set();
	const labelFor = (el) => {
		if (el.id) { const l = document.querySelector('label[for="' + CSS.escape(el.id) + '"]'); if (l && l.innerText) return l.innerText.trim(); }
		if (el.getAttribute('aria-label')) return el.getAttribute('aria-label').trim();
		const lab = el.closest('label'); if (lab && lab.innerText) return lab.innerText.trim();
		const wrap = el.closest('div'); if (wrap) { const l = wrap.querySelector('label'); if (l && l.innerText) return l.innerText.trim(); }
		return '';
	};
	// The question text for a grouped control (radios): prefer a fieldset
	// legend, else a label/legend in the wrapping container.
	const groupLabel = (el) => {
		const fs = el.closest('fieldset');
		if (fs) { const lg = fs.querySelector('legend'); if (lg && lg.innerText) return lg.innerText.trim(); }
		const wrap = el.closest('div'); if (wrap) { const l = wrap.querySelector('label, legend'); if (l && l.innerText) return l.innerText.trim(); }
		return labelFor(el);
	};
	const sel = (el) => el.id ? '#' + CSS.escape(el.id) : (el.name ? '[name="' + CSS.escape(el.name) + '"]' : '');
	document.querySelectorAll('input, textarea, select').forEach(el => {
		const tag = el.tagName.toLowerCase();
		const type = (el.type || tag).toLowerCase();
		if (['hidden','submit','button','file','search','reset','image'].includes(type)) return;

		// Radios: collapse a same-name group into one field carrying the
		// option labels, so the model picks one value.
		if (type === 'radio' && el.name) {
			const key = 'radio:' + el.name;
			if (seen.has(key)) return; seen.add(key);
			const group = [...document.querySelectorAll('input[type=radio][name="' + CSS.escape(el.name) + '"]')];
			out.push({ selector: '[name="' + CSS.escape(el.name) + '"]', label: (groupLabel(el) || '').slice(0,160), type: 'radio', required: group.some(r => r.required), options: group.map(r => labelFor(r) || r.value).filter(Boolean).slice(0,40) });
			return;
		}

		const s = sel(el); if (!s) return;
		const role = el.getAttribute('role') || '';
		let t = tag === 'select' ? 'select' : type;
		if (role === 'combobox' || el.getAttribute('aria-autocomplete') === 'list' || el.getAttribute('aria-haspopup') === 'listbox') t = 'combobox';
		const f = { selector: s, label: (labelFor(el) || '').slice(0,160), type: t, required: !!(el.required || el.getAttribute('aria-required') === 'true') };
		if (tag === 'select') f.options = [...el.options].map(o => (o.text || '').trim()).filter(Boolean).slice(0,40);
		out.push(f);
	});
	return JSON.stringify(out);
}`

// GetFormFields navigates to url and returns the live form's fillable
// controls as structured descriptors (real selectors + labels), so an LLM
// can answer custom questions against actual selectors instead of guessing
// from truncated HTML.
func (p *Pool) GetFormFields(ctx context.Context, url string) ([]FieldDescriptor, error) {
	inst, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer p.release(inst)

	inst.mu.Lock()
	defer inst.mu.Unlock()

	if err := inst.ensure(); err != nil {
		return nil, err
	}

	page, err := inst.browser.Context(ctx).Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		_ = inst.browser.Close()
		inst.browser = nil
		telemetry.RecordAutoApplyBrowserRestart("crashed")
		return nil, fmt.Errorf("browser: new page: %w", err)
	}
	defer func() { _ = page.Close() }()

	page = page.Context(ctx).Timeout(p.timeout)
	if err := page.SetUserAgent(&proto.NetworkSetUserAgentOverride{UserAgent: p.userAgent}); err != nil {
		return nil, fmt.Errorf("browser: set user agent: %w", err)
	}
	if err := page.Navigate(url); err != nil {
		return nil, fmt.Errorf("browser: navigate: %w", err)
	}
	settle(page)

	res, err := page.Eval(fieldExtractJS)
	if err != nil {
		return nil, fmt.Errorf("browser: extract fields: %w", err)
	}
	var fields []FieldDescriptor
	if err := json.Unmarshal([]byte(res.Value.Str()), &fields); err != nil {
		return nil, fmt.Errorf("browser: decode fields: %w", err)
	}
	return fields, nil
}

// verifySubmit checks the post-submit page for one of the configured
// fillField sets one form control, choosing the right interaction for its
// kind: native <select> via Select, react-select/combobox via open-type-pick,
// and plain inputs/textareas via Input. Best-effort — a missing or stubborn
// field is skipped silently (non-required fields are common).
func fillField(page *rod.Page, sel, val string) {
	el, err := page.Timeout(5 * time.Second).Element(sel)
	if err != nil {
		return
	}
	typ := strings.ToLower(attr(el, "type"))
	switch {
	case elTag(el) == "select":
		_ = el.Select([]string{val}, true, rod.SelectorTypeText)
	case typ == "radio":
		setRadioGroup(page, el, val)
	case typ == "checkbox":
		setChecked(el, truthy(val))
	case isCombobox(el):
		fillReactSelect(page, el, val)
	default: // text, email, tel, url, number, date, password, textarea, …
		_ = el.SelectAllText()
		_ = el.Input(val)
	}
}

// setChecked clicks a checkbox to match the desired state, only when it
// currently differs (so it never toggles an already-correct box).
func setChecked(el *rod.Element, want bool) {
	checked := false
	if p, err := el.Property("checked"); err == nil {
		checked = p.Bool()
	}
	if want != checked {
		_ = el.Click(proto.InputMouseButtonLeft, 1)
	}
}

// setRadioGroup clicks the radio in el's name-group whose label or value
// matches val. el is any radio in the group (the [name=...] selector
// resolves to the first one).
func setRadioGroup(page *rod.Page, el *rod.Element, val string) {
	name := attr(el, "name")
	if name == "" {
		// Lone radio with no group: click it when the value is affirmative.
		if truthy(val) {
			_ = el.Click(proto.InputMouseButtonLeft, 1)
		}
		return
	}
	radios, err := page.Elements(`input[type="radio"][name="` + cssEscapeAttr(name) + `"]`)
	if err != nil {
		return
	}
	var contains *rod.Element
	for _, r := range radios {
		lbl := radioLabel(r)
		rv := attr(r, "value")
		if strings.EqualFold(strings.TrimSpace(lbl), val) || strings.EqualFold(rv, val) {
			_ = r.Click(proto.InputMouseButtonLeft, 1)
			return
		}
		if contains == nil && lbl != "" && strings.Contains(strings.ToLower(lbl), strings.ToLower(val)) {
			contains = r
		}
	}
	if contains != nil {
		_ = contains.Click(proto.InputMouseButtonLeft, 1)
	}
}

// radioLabel returns the visible label text associated with a radio input.
func radioLabel(el *rod.Element) string {
	obj, err := el.Eval(`() => {
		if (this.id) { const l = document.querySelector('label[for="' + CSS.escape(this.id) + '"]'); if (l && l.innerText) return l.innerText.trim(); }
		const lab = this.closest('label'); if (lab && lab.innerText) return lab.innerText.trim();
		if (this.getAttribute('aria-label')) return this.getAttribute('aria-label').trim();
		return this.value || '';
	}`)
	if err != nil {
		return ""
	}
	return obj.Value.Str()
}

// cssEscapeAttr escapes a value for safe inclusion in a CSS attribute
// selector string (the common metacharacters in form names).
func cssEscapeAttr(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return r.Replace(s)
}

func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "y", "1", "on", "checked", "agree", "i agree", "confirm", "acknowledge":
		return true
	}
	return false
}

// elTag returns the lowercased tag name of an element.
func elTag(el *rod.Element) string {
	obj, err := el.Eval(`() => this.tagName.toLowerCase()`)
	if err != nil {
		return ""
	}
	return obj.Value.Str()
}

// isCombobox reports whether el is a custom dropdown (e.g. react-select),
// which presents as a text input but needs open → type → pick to commit.
func isCombobox(el *rod.Element) bool {
	switch attr(el, "role") {
	case "combobox", "listbox":
		return true
	}
	return attr(el, "aria-haspopup") == "listbox" || attr(el, "aria-autocomplete") == "list"
}

// fillReactSelect commits a value in a react-select-style dropdown: focus
// to open the menu, type to filter, then click the option whose text
// matches. It NEVER presses Enter — in a form field that can trigger a
// submission. When no option matches it presses Escape to close the menu
// and leaves the field unset (safer than a wrong or accidental submit).
func fillReactSelect(page *rod.Page, el *rod.Element, val string) {
	_ = el.Click(proto.InputMouseButtonLeft, 1)
	_ = el.Input(val)

	// Scope the option lookup to THIS combobox's own listbox (via the
	// input's aria-controls/aria-owns). Without scoping, a previously
	// filled combobox whose menu is still mounted leaks its options into
	// the query and the match lands on the wrong widget (or not at all) —
	// which is exactly why the location field wasn't committing.
	optSel := `[role="option"], [id*="-option-"], [class*="select__option"], [class*="-option"], .pac-item, li[role="option"]`
	if lb := attr(el, "aria-controls"); lb == "" {
		if lb = attr(el, "aria-owns"); lb != "" {
			optSel = `[id="` + cssEscapeAttr(lb) + `"] [role="option"], [id="` + cssEscapeAttr(lb) + `"] li`
		}
	} else {
		optSel = `[id="` + cssEscapeAttr(lb) + `"] [role="option"], [id="` + cssEscapeAttr(lb) + `"] li`
	}

	// Option menus render asynchronously — react-select filters in a tick,
	// location autocompletes fetch over the network. Poll up to ~4s rather
	// than guessing a single delay.
	var opts []*rod.Element
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		opts, _ = page.Elements(optSel)
		if len(opts) > 0 {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	// Prefer an exact (case-insensitive) match, then a contains match.
	var contains *rod.Element
	for _, o := range opts {
		txt, terr := o.Text()
		if terr != nil {
			continue
		}
		t := strings.TrimSpace(txt)
		if strings.EqualFold(t, val) {
			_ = o.Click(proto.InputMouseButtonLeft, 1)
			return
		}
		if contains == nil && t != "" && strings.Contains(strings.ToLower(t), strings.ToLower(val)) {
			contains = o
		}
	}
	if contains != nil {
		_ = contains.Click(proto.InputMouseButtonLeft, 1)
		return
	}
	_ = el.Type(input.Escape) // close the menu; never submit
}

// settle waits for the page to become usable after a navigation or
// action: the load event plus a bounded DOM-stability window. It
// deliberately avoids WaitStable (network-idle), which never settles on
// modern ATS pages with continuous analytics/beacons and would burn the
// entire page deadline before any field could be filled.
func settle(page *rod.Page) {
	_ = page.WaitLoad()
	_ = page.Timeout(8*time.Second).WaitDOMStable(time.Second, 0)
}

// captureForm saves a PNG of the application form (the <form> with the
// most fields) at full resolution, so filled values are legible. Falls
// back to a full-page screenshot when no form is found.
func captureForm(page *rod.Page, path string) {
	marked, err := page.Eval(`() => {
		const forms = [...document.querySelectorAll('form')];
		if (!forms.length) return false;
		let best = forms[0], n = -1;
		for (const f of forms) {
			const c = f.querySelectorAll('input,select,textarea').length;
			if (c > n) { n = c; best = f; }
		}
		best.id = '__autoapply_form';
		return true;
	}`)
	if err == nil && marked.Value.Bool() {
		if el, e := page.Element("#__autoapply_form"); e == nil {
			_ = el.ScrollIntoView()
			if data, se := el.Screenshot(proto.PageCaptureScreenshotFormatPng, 0); se == nil {
				_ = os.WriteFile(path, data, 0o644)
				return
			}
		}
	}
	if data, se := page.Screenshot(true, nil); se == nil {
		_ = os.WriteFile(path, data, 0o644)
	}
}

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
	defer func() { _ = f.Close() }()
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
