# Line-by-Line Explanations - Part 2: Submitter Implementations

---

## pkg/autoapply/ats/greenhouse.go

```go
package ats

import (
    "context"
    "errors"
    "strings"

    "github.com/stawi-opportunities/opportunities/pkg/autoapply"
    "github.com/stawi-opportunities/opportunities/pkg/autoapply/browser"
    "github.com/stawi-opportunities/opportunities/pkg/domain"
)

type GreenhouseSubmitter struct {
    client browser.ApplyClient
}

func NewGreenhouseSubmitter(client browser.ApplyClient) *GreenhouseSubmitter {
    return &GreenhouseSubmitter{client: client}
}
```
Struct holds the shared browser client pool. Constructor injection (dependency injection pattern).

```go
func (s *GreenhouseSubmitter) Name() string { return "greenhouse_ui" }
```
Returns submitter name for logs/metrics (low-cardinality).

```go
func (s *GreenhouseSubmitter) CanHandle(sourceType domain.SourceType, applyURL string) bool {
    if sourceType == domain.SourceGreenhouse {
        return true
    }
    lower := strings.ToLower(applyURL)
    return strings.Contains(lower, "boards.greenhouse.io") ||
        strings.Contains(lower, "gh_jid=") ||
        strings.Contains(lower, "greenhouse.io/jobs")
}
```
**Detection logic** (matches on both explicit source type AND URL heuristics):
- If source type is explicitly "greenhouse": YES
- If URL contains "boards.greenhouse.io": YES
- If URL contains "gh_jid=" (query param): YES
- If URL contains "greenhouse.io/jobs": YES

This allows handling Greenhouse URLs even if the source type is misclassified.

```go
func (s *GreenhouseSubmitter) Submit(ctx context.Context, req autoapply.SubmitRequest) (autoapply.SubmitResult, error) {
    firstName, lastName := splitName(req.FullName)

    textFields := map[string]string{
        "#first_name":                            firstName,
        "#last_name":                             lastName,
        "#email":                                 req.Email,
        "#phone":                                 req.Phone,
        "#job_application_first_name":            firstName,
        "#job_application_last_name":             lastName,
        "#job_application_email":                 req.Email,
        "#job_application_phone":                 req.Phone,
    }
```
Maps Greenhouse form fields:
- **Duplicate selectors**: Greenhouse sometimes uses `#first_name` or `#job_application_first_name` (version differences)
- By including both, the browser client tries all and skips missing ones (non-required fields)

```go
    if req.CoverLetter != "" {
        textFields["#cover_letter_text"] = req.CoverLetter
        textFields["#job_application_cover_letter_text"] = req.CoverLetter
    }

    err := s.client.FillAndSubmit(ctx, browser.SubmitOptions{
        URL:         req.ApplyURL,
        TextFields:  textFields,
        FileField:   "#resume, #job_application_resume",
        FileBytes:   req.CVBytes,
        FileName:    req.CVFilename,
        SubmitSel:   "[data-submit='true'], input[type='submit'], button[type='submit']",
        ConfirmText: "thank you for applying",
    })
```
Calls browser to fill & submit:
- **ConfirmText**: "thank you for applying" — Greenhouse's standard confirmation message
- **SubmitSel**: Multiple selector variants (Greenhouse uses data-submit, generic submit buttons, etc.)

```go
    if err != nil {
        if errors.Is(err, browser.ErrCAPTCHA) {
            return autoapply.SubmitResult{Method: "skipped", SkipReason: "captcha"}, nil
        }
        if errors.Is(err, browser.ErrElementNotFound) {
            return autoapply.SubmitResult{Method: "skipped", SkipReason: "unsupported"}, nil
        }
        if errors.Is(err, browser.ErrSubmitNotConfirmed) {
            return autoapply.SubmitResult{Method: "skipped", SkipReason: "not_confirmed"}, nil
        }
        return autoapply.SubmitResult{}, err
    }

    return autoapply.SubmitResult{Method: "ats_ui"}, nil
}
```
**Error classification**:
- **ErrCAPTCHA**: Terminal → skip/captcha (ack, don't retry)
- **ErrElementNotFound**: Terminal → skip/unsupported (page structure changed, ack)
- **ErrSubmitNotConfirmed**: Terminal → skip/not_confirmed (submit clicked but no confirmation, ack)
- **Other errors**: Transient (return error for queue redelivery)

---

## pkg/autoapply/ats/lever.go

```go
func (s *LeverSubmitter) CanHandle(sourceType domain.SourceType, applyURL string) bool {
    if sourceType == domain.SourceLever {
        return true
    }
    return strings.Contains(strings.ToLower(applyURL), "jobs.lever.co") ||
        strings.Contains(strings.ToLower(applyURL), "lever.co/")
}
```
**Lever detection**:
- Source type "lever": YES
- URL contains "jobs.lever.co": YES
- URL contains "lever.co/": YES (catches all Lever domains)

```go
func (s *LeverSubmitter) Submit(ctx context.Context, req autoapply.SubmitRequest) (autoapply.SubmitResult, error) {
    textFields := map[string]string{
        "[name='name']":  req.FullName,
        "[name='email']": req.Email,
        "[name='phone']": req.Phone,
    }
```
**Note**: Lever has `[name='org']` field (candidate's current company) that we don't populate. Would need to add it to SubmitRequest in future.

```go
    if req.CoverLetter != "" {
        textFields["[name='comments']"] = req.CoverLetter
    }

    err := s.client.FillAndSubmit(ctx, browser.SubmitOptions{
        URL:         req.ApplyURL,
        TextFields:  textFields,
        FileField:   "[name='resume']",
        FileBytes:   req.CVBytes,
        FileName:    req.CVFilename,
        SubmitSel:   "[data-qa='btn-submit-application'], button[type='submit']",
        ConfirmText: "application submitted",
    })
```
**Key differences from Greenhouse**:
- Uses `[name='...']` attribute selectors (not IDs)
- Confirm text: "application submitted"
- Submit selector: `[data-qa='btn-submit-application']` (Lever's data-qa convention)

Rest of error handling is identical to Greenhouse.

---

## pkg/autoapply/ats/workday.go

```go
func (s *WorkdaySubmitter) CanHandle(sourceType domain.SourceType, applyURL string) bool {
    if sourceType == domain.SourceWorkday {
        return true
    }
    lower := strings.ToLower(applyURL)
    return strings.Contains(lower, "myworkdayjobs.com") ||
        strings.Contains(lower, "myworkday.com")
}
```
**Workday detection**:
- Source type "workday": YES
- URL contains "myworkdayjobs.com": YES
- URL contains "myworkday.com": YES (covers both variants)

```go
func (s *WorkdaySubmitter) Submit(ctx context.Context, req autoapply.SubmitRequest) (autoapply.SubmitResult, error) {
    firstName, lastName := splitName(req.FullName)

    textFields := map[string]string{
        "[data-automation-id='firstName']":    firstName,
        "[data-automation-id='lastName']":     lastName,
        "[data-automation-id='email']":        req.Email,
        "[data-automation-id='phone-number']": req.Phone,
    }
```
**Workday uses data-automation-id attributes** (not traditional form names or IDs).

```go
    err := s.client.FillAndSubmit(ctx, browser.SubmitOptions{
        URL:        req.ApplyURL,
        TextFields: textFields,
        FileField:  "[data-automation-id='file-upload-input-ref']",
        FileBytes:  req.CVBytes,
        FileName:   req.CVFilename,
        SubmitSel:  "[data-automation-id='bottom-navigation-next-button'], button[data-automation-id='saveAndContinueButton']",
        ConfirmSel: "[data-automation-id='progressBar'], [data-automation-id='applicationProgress']",
    })
```
**Multi-step flow**:
- **SubmitSel**: "Next" button (Workday apps are multi-step)
- **ConfirmSel**: Progress bar indicators (multi-step confirmation)
- Multiple selector variants for different Workday versions

Note: No ConfirmText (relies on selector only).

---

## pkg/autoapply/ats/smartrecruiters.go

```go
func (s *SmartRecruitersSubmitter) CanHandle(sourceType domain.SourceType, applyURL string) bool {
    if sourceType == domain.SourceSmartRecruitersPage || sourceType == domain.SourceSmartRecruitersAPI {
        return true
    }
    return strings.Contains(strings.ToLower(applyURL), "smartrecruiters.com")
}
```
**SmartRecruiters detection**:
- Source type "smartrecruiters_page" or "smartrecruiters_api": YES
- URL contains "smartrecruiters.com": YES

```go
func (s *SmartRecruitersSubmitter) Submit(ctx context.Context, req autoapply.SubmitRequest) (autoapply.SubmitResult, error) {
    firstName, lastName := splitName(req.FullName)

    textFields := map[string]string{
        "[name='firstName']":   firstName,
        "[name='lastName']":    lastName,
        "[name='email']":       req.Email,
        "[name='phoneNumber']": req.Phone,
    }

    err := s.client.FillAndSubmit(ctx, browser.SubmitOptions{
        URL:         req.ApplyURL,
        TextFields:  textFields,
        FileField:   "input[type='file']",
        FileBytes:   req.CVBytes,
        FileName:    req.CVFilename,
        SubmitSel:   "[data-test-id='apply-button-bottom'], button[type='submit']",
        ConfirmText: "application sent",
    })
```
**SmartRecruiters specifics**:
- Uses `[name='...']` attributes
- File field: generic `input[type='file']`
- Confirm text: "application sent"
- Submit selector: `[data-test-id='apply-button-bottom']` (SmartRecruiters' testing convention)

---

## pkg/autoapply/ats/canhandle_test.go (Helper)

```go
func splitName(full string) (string, string) {
    full = strings.TrimSpace(full)
    idx := strings.Index(full, " ")
    if idx < 0 {
        return full, ""
    }
    return full[:idx], strings.TrimSpace(full[idx+1:])
}
```
Splits "First Last" into (first, last):
- Trims whitespace
- Finds first space
- If no space: returns (full, "")
- If space found: splits at space
- Example: "Jane Doe" → ("Jane", "Doe")
- Example: "Anne Marie Smith" → ("Anne", "Marie Smith") (multi-word last names preserved)

---

## pkg/autoapply/sessionsubmitter/submitter.go

```go
package sessionsubmitter

import (
    "bytes"
    "context"
    "errors"
    "fmt"
    "io"
    "mime/multipart"
    "net/http"
    "net/http/cookiejar"
    "net/url"
    "strings"
    "time"

    "github.com/pitabwire/util"
    "golang.org/x/net/html"

    "github.com/stawi-opportunities/opportunities/pkg/authmanifest"
    "github.com/stawi-opportunities/opportunities/pkg/authsession"
    "github.com/stawi-opportunities/opportunities/pkg/autoapply"
    "github.com/stawi-opportunities/opportunities/pkg/domain"
)

const (
    ReasonSessionRequired = "session_required"
    ReasonSessionExpired  = "session_expired"
    ReasonFormChanged     = "form_changed"
    ReasonCaptcha         = "captcha"
    ReasonUnsupportedFlow = "unsupported_flow"
    ReasonNoApplyURL      = "no_apply_url"
)

type Submitter struct {
    manifests ManifestStore
    sessions  authsession.SessionProvider
    client    *http.Client
}
```
**Skip reasons** (low-cardinality for metrics):
- **session_required**: Candidate hasn't captured session for this source (prompt reconnect)
- **session_expired**: Session is stale (revoked and candidate notified)
- **form_changed**: Form structure different than expected (manifest out of sync)
- **captcha**: CAPTCHA detected in HTML
- **unsupported_flow**: Source has non-http-form flow (e.g., OAuth only)
- **no_apply_url**: URL is empty

```go
type ManifestStore interface {
    Lookup(sourceType domain.SourceType) (*authmanifest.Manifest, bool)
}

type Config struct {
    Manifests   ManifestStore
    Sessions    authsession.SessionProvider
    HTTPTimeout time.Duration
}

func New(cfg Config) *Submitter {
    timeout := cfg.HTTPTimeout
    if timeout == 0 {
        timeout = 30 * time.Second
    }
    return &Submitter{
        manifests: cfg.Manifests,
        sessions:  cfg.Sessions,
        client:    &http.Client{Timeout: timeout},
    }
}
```
Constructor takes manifests, sessions, and timeout.

```go
func (s *Submitter) Name() string { return "session_replay" }

func (s *Submitter) CanHandle(sourceType domain.SourceType, applyURL string) bool {
    m, ok := s.manifests.Lookup(sourceType)
    if !ok {
        return false
    }
    if m.AuthMethod != authmanifest.AuthExtension {
        return false
    }
    if m.ApplyFlow.Type != authmanifest.ApplyHTTPForm {
        return false
    }
    return m.MatchesApplyURL(applyURL)
}
```
**Preconditions for handling**:
1. Source must have a manifest
2. Manifest must use extension auth (browser captured session)
3. Apply flow must be HTTP form (not OAuth, not JS-heavy)
4. URL must match manifest's apply_url_pattern

```go
func (s *Submitter) Submit(ctx context.Context, req autoapply.SubmitRequest) (autoapply.SubmitResult, error) {
    log := util.Log(ctx).
        WithField("submitter", s.Name()).
        WithField("source_type", req.SourceType).
        WithField("candidate_id", req.CandidateID)

    if req.ApplyURL == "" {
        return autoapply.SubmitResult{Method: "skipped", SkipReason: ReasonNoApplyURL}, nil
    }
    m, ok := s.manifests.Lookup(req.SourceType)
    if !ok || m.ApplyFlow.Type != authmanifest.ApplyHTTPForm {
        return autoapply.SubmitResult{Method: "skipped", SkipReason: ReasonUnsupportedFlow}, nil
    }

    sess, err := s.sessions.Session(ctx, req.CandidateID, req.SourceType)
    if errors.Is(err, authsession.ErrSessionRequired) {
        return autoapply.SubmitResult{Method: "skipped", SkipReason: ReasonSessionRequired}, nil
    }
    if errors.Is(err, authsession.ErrSessionExpired) {
        return autoapply.SubmitResult{Method: "skipped", SkipReason: ReasonSessionExpired}, nil
    }
    if err != nil {
        return autoapply.SubmitResult{}, fmt.Errorf("session lookup: %w", err)
    }
```
**Session lookup**:
- If ErrSessionRequired: candidate hasn't connected extension (ack, signal UI to prompt reconnect)
- If ErrSessionExpired: session is stale (ack, signal UI to prompt re-auth)
- If other error: transient (return error for redelivery)

```go
    client, err := s.clientFromSession(sess, m)
    if err != nil {
        return autoapply.SubmitResult{}, fmt.Errorf("session-bound client: %w", err)
    }

    getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.ApplyURL, nil)
    if err != nil {
        return autoapply.SubmitResult{Method: "skipped", SkipReason: ReasonNoApplyURL}, nil
    }
    applySessionHeaders(getReq, sess)

    getResp, err := client.Do(getReq)
    if err != nil {
        return autoapply.SubmitResult{}, fmt.Errorf("apply GET: %w", err)
    }
    defer getResp.Body.Close()

    if loggedOut := looksLoggedOut(getResp, m); loggedOut {
        _ = s.sessions.Revoke(ctx, req.CandidateID, req.SourceType)
        log.WithField("status", getResp.StatusCode).Info("session_replay: detected logged-out on GET; revoked session")
        return autoapply.SubmitResult{Method: "skipped", SkipReason: ReasonSessionExpired}, nil
    }

    getBody, err := io.ReadAll(io.LimitReader(getResp.Body, 4<<20))
    if err != nil {
        return autoapply.SubmitResult{}, fmt.Errorf("apply GET read: %w", err)
    }

    csrfToken, csrfHeader := extractCSRF(m, client, getBody, req.ApplyURL)

    contentType, body, err := buildMultipart(m, req, csrfToken)
    if err != nil {
        log.WithError(err).Warn("session_replay: build multipart")
        return autoapply.SubmitResult{Method: "skipped", SkipReason: ReasonFormChanged}, nil
    }

    postURL := postURLFromForm(getBody, req.ApplyURL)

    postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, body)
    if err != nil {
        return autoapply.SubmitResult{}, fmt.Errorf("apply POST build: %w", err)
    }
    postReq.Header.Set("Content-Type", contentType)
    postReq.Header.Set("Referer", req.ApplyURL)
    applySessionHeaders(postReq, sess)
    if csrfHeader != "" {
        postReq.Header.Set(csrfHeader, csrfToken)
    }

    postResp, err := client.Do(postReq)
    if err != nil {
        return autoapply.SubmitResult{}, fmt.Errorf("apply POST: %w", err)
    }
    defer postResp.Body.Close()
```

**Complete HTTP session replay flow**:
1. Create HTTP client with captured cookies
2. GET the apply URL (preserves session cookies)
3. Check if logged out (3xx to /login, 401, etc.)
4. Parse response HTML for CSRF token
5. Extract form post URL from HTML
6. Build multipart POST body from manifest field map + candidate data
7. POST with captured cookies + CSRF token + headers
8. Return result

---

## pkg/autoapply/brightermondaysubmitter/submitter.go

```go
package brightermondaysubmitter

import (
    "bytes"
    "context"
    "errors"
    "fmt"
    "io"
    "mime/multipart"
    "net/http"
    "net/http/cookiejar"
    "net/url"
    "strings"
    "time"

    "github.com/pitabwire/util"
    "golang.org/x/net/html"

    "github.com/stawi-opportunities/opportunities/pkg/authsession"
    "github.com/stawi-opportunities/opportunities/pkg/autoapply"
    "github.com/stawi-opportunities/opportunities/pkg/domain"
)

const (
    enquiryFormID    = "enquiry-form"
    bmOrigin         = "https://www.brightermonday.co.ke"
    enquiriesIndexURL = "https://www.brightermonday.co.ke/account/customer/enquiries"
    defaultTimeout   = 30 * time.Second
)

type Submitter struct {
    sessions authsession.SessionProvider
    timeout  time.Duration
}

func (s *Submitter) Name() string { return "brightermonday" }

func (s *Submitter) CanHandle(sourceType domain.SourceType, applyURL string) bool {
    if sourceType != domain.SourceBrighterMonday {
        return false
    }
    return strings.HasPrefix(applyURL, bmOrigin+"/listings/")
}
```
**BM-specific detection**:
- Must be BrighterMonday source type
- URL must start with `https://www.brightermonday.co.ke/listings/`

```go
func (s *Submitter) Submit(ctx context.Context, req autoapply.SubmitRequest) (autoapply.SubmitResult, error) {
    log := util.Log(ctx).WithField("submitter", s.Name()).WithField("candidate_id", req.CandidateID)

    sess, err := s.sessions.Session(ctx, req.CandidateID, req.SourceType)
    if errors.Is(err, authsession.ErrSessionRequired) {
        return autoapply.SubmitResult{Method: "skipped", SkipReason: "session_required"}, nil
    }
    if errors.Is(err, authsession.ErrSessionExpired) {
        return autoapply.SubmitResult{Method: "skipped", SkipReason: "session_expired"}, nil
    }
    if err != nil {
        return autoapply.SubmitResult{}, fmt.Errorf("session lookup: %w", err)
    }

    client, err := newClientFromSession(s.timeout, sess)
    if err != nil {
        return autoapply.SubmitResult{}, fmt.Errorf("session-bound client: %w", err)
    }

    getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.ApplyURL, nil)
    if err != nil {
        return autoapply.SubmitResult{Method: "skipped", SkipReason: "no_apply_url"}, nil
    }
    applyBrowserHeaders(getReq, sess, "")

    getResp, err := client.Do(getReq)
    if err != nil {
        return autoapply.SubmitResult{}, fmt.Errorf("listing GET: %w", err)
    }
    defer getResp.Body.Close()

    if loggedOut(getResp) {
        _ = s.sessions.Revoke(ctx, req.CandidateID, req.SourceType)
        log.Info("brightermonday: detected logged-out on listing GET; revoked session")
        return autoapply.SubmitResult{Method: "skipped", SkipReason: "session_expired"}, nil
    }

    body, err := io.ReadAll(io.LimitReader(getResp.Body, 8<<20))
    if err != nil {
        return autoapply.SubmitResult{}, fmt.Errorf("listing read: %w", err)
    }

    form, err := extractEnquiryForm(body)
    if err != nil {
        log.WithError(err).Warn("brightermonday: enquiry form not found")
        return autoapply.SubmitResult{Method: "skipped", SkipReason: "form_changed"}, nil
    }

    // Override cover letter with what the intent carries. BM requires
    // `description` to be non-empty (required="required" on the
    // textarea).
    coverLetter := strings.TrimSpace(req.CoverLetter)
```

**BrighterMonday-specific flow**:
1. Get session (same as generic replay)
2. GET the listing page
3. Parse HTML for `<form id="enquiry-form">`
4. Extract all hidden inputs and selected options
5. Override `description` field with candidate's cover letter

Key insight: BM's form is mostly hidden fields pre-populated from candidate's BM profile. We replay them verbatim + override cover letter.

---

## pkg/autoapply/llm_form.go

```go
package autoapply

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "regexp"
    "strings"

    "github.com/stawi-opportunities/opportunities/pkg/autoapply/browser"
    "github.com/stawi-opportunities/opportunities/pkg/domain"
)

type LLMClient interface {
    Complete(ctx context.Context, system, user string) (string, error)
}

type LLMFormSubmitter struct {
    client browser.ApplyClient
    llm    LLMClient
}

func NewLLMFormSubmitter(client browser.ApplyClient, llm LLMClient) *LLMFormSubmitter {
    return &LLMFormSubmitter{client: client, llm: llm}
}

func (s *LLMFormSubmitter) Name() string { return "llm_form" }

func (s *LLMFormSubmitter) CanHandle(_ domain.SourceType, applyURL string) bool {
    return strings.TrimSpace(applyURL) != "" && s.llm != nil
}
```
**LLM submitter**:
- Tier order: comes AFTER all ATS-specific and session-replay tiers
- CanHandle: catch-all (any non-empty URL, if LLM is configured)
- Only used if no earlier tier matched

```go
var systemPrompt = `You are a job application form assistant. Given simplified form HTML and a candidate profile, return a JSON object mapping CSS selectors to values that should be filled. Only include fields you can confidently fill from the profile. Do not include file upload inputs. Respond with valid JSON only, no markdown.`

func (s *LLMFormSubmitter) Submit(ctx context.Context, req SubmitRequest) (SubmitResult, error) {
    html, err := s.client.GetHTML(ctx, req.ApplyURL)
    if err != nil {
        return SubmitResult{}, fmt.Errorf("llm_form: get html: %w", err)
    }

    formHTML := extractFormHTML(html, 4000)
    if formHTML == "" {
        return SubmitResult{Method: "skipped", SkipReason: "no_form_found"}, nil
    }

    userPrompt := fmt.Sprintf(`Candidate profile:
- Name: %s
- Email: %s
- Phone: %s
- Location: %s
- Current title: %s
- Skills: %s

Form HTML:
%s

Return JSON: {"selector": "value", ...}`,
        req.FullName, req.Email, req.Phone, req.Location, req.CurrentTitle, req.Skills, formHTML)

    reply, err := s.llm.Complete(ctx, systemPrompt, userPrompt)
    if err != nil {
        return SubmitResult{}, fmt.Errorf("llm_form: complete: %w", err)
    }

    fieldMap, err := parseFieldMap(reply)
    if err != nil || len(fieldMap) == 0 {
        return SubmitResult{Method: "skipped", SkipReason: "llm_no_fields"}, nil
    }

    fillErr := s.client.FillAndSubmit(ctx, browser.SubmitOptions{
        URL:        req.ApplyURL,
        TextFields: fieldMap,
        FileField:  "input[type='file']",
        FileBytes:  req.CVBytes,
        FileName:   req.CVFilename,
        SubmitSel:  "button[type='submit'], input[type='submit']",
    })
    if fillErr != nil {
        if errors.Is(fillErr, browser.ErrCAPTCHA) {
            return SubmitResult{Method: "skipped", SkipReason: "captcha"}, nil
        }
        if errors.Is(fillErr, browser.ErrElementNotFound) {
            return SubmitResult{Method: "skipped", SkipReason: "unsupported"}, nil
        }
        if errors.Is(fillErr, browser.ErrSubmitNotConfirmed) {
            return SubmitResult{Method: "skipped", SkipReason: "not_confirmed"}, nil
        }
        return SubmitResult{}, fillErr
    }

    return SubmitResult{Method: "llm_form"}, nil
}
```

**LLM Flow**:
1. Fetch apply page HTML
2. Extract form HTML (heuristics: pick highest-scoring form)
3. Truncate to 4000 runes (token budget)
4. Send to LLM: "Here's HTML and candidate data, map CSS selectors to fill"
5. Parse LLM response as JSON
6. If no fields: skip/llm_no_fields
7. Use inferred fields to fill & submit

```go
var formStartRE = regexp.MustCompile(`(?i)<form\b`)

func extractFormHTML(html string, maxLen int) string {
    type candidate struct {
        body  string
        score int
    }
    var picks []candidate

    for _, idx := range formStartRE.FindAllStringIndex(html, -1) {
        start := idx[0]
        rest := strings.ToLower(html[start:])
        end := strings.Index(rest, "</form>")
        if end < 0 {
            continue
        }
        body := html[start : start+end+len("</form>")]
        picks = append(picks, candidate{body: body, score: scoreForm(body)})
    }
    if len(picks) == 0 {
        return ""
    }

    best := picks[0]
    for _, p := range picks[1:] {
        if p.score > best.score {
            best = p
        }
    }

    snippet := stripTag(best.body, "script")
    snippet = stripTag(snippet, "style")

    if len(snippet) > maxLen {
        runes := []rune(snippet)
        if len(runes) > maxLen {
            runes = runes[:maxLen]
        }
        snippet = string(runes)
    }
    return snippet
}
```

**Form extraction logic**:
1. Find all `<form ...>` tags (case-insensitive)
2. For each form: find matching `</form>`
3. Score each form (inputs, selects, keywords vs anti-keywords)
4. Pick highest-scoring form (likely the application form, not search/login)
5. Strip `<script>` and `<style>` tags (reduce noise)
6. Truncate to maxLen runes (avoid splitting UTF-8)

```go
func scoreForm(body string) int {
    lower := strings.ToLower(body)
    score := 0
    score += strings.Count(lower, "<input") * 2
    score += strings.Count(lower, "<textarea") * 3
    score += strings.Count(lower, "<select") * 2
    for _, kw := range []string{"apply", "application", "resume", "cv", "cover letter", "first name", "last name"} {
        if strings.Contains(lower, kw) {
            score += 5
        }
    }
    for _, anti := range []string{"search", "subscribe", "newsletter", "login", "sign in"} {
        if strings.Contains(lower, anti) {
            score -= 4
        }
    }
    return score
}
```

**Scoring heuristics**:
- **Boost**: Each input (2 pts), textarea (3 pts), select (2 pts)
- **Keywords**: "apply", "application", "resume", "cv", "cover letter", etc. (+5 each)
- **Anti-keywords**: "search", "subscribe", "newsletter", "login", etc. (-4 each)

Purpose: Prioritize application forms over search/login/newsletter forms common on careers pages.

```go
func stripTag(s, tag string) string {
    open := "<" + strings.ToLower(tag)
    closeT := "</" + strings.ToLower(tag) + ">"
    var b strings.Builder
    b.Grow(len(s))
    i := 0
    lower := strings.ToLower(s)
    for i < len(s) {
        o := strings.Index(lower[i:], open)
        if o < 0 {
            b.WriteString(s[i:])
            break
        }
        b.WriteString(s[i : i+o])
        c := strings.Index(lower[i+o:], closeT)
        if c < 0 {
            break
        }
        i = i + o + c + len(closeT)
    }
    return b.String()
}
```

**Tag stripping**:
1. Find next opening tag (case-insensitive)
2. Write everything before it
3. Find matching closing tag
4. Skip the entire tag + content
5. Continue with rest of string

---

## pkg/autoapply/email_fallback.go

```go
package autoapply

import (
    "bytes"
    "context"
    "encoding/base64"
    "fmt"
    "mime"
    "mime/multipart"
    "net/mail"
    "net/smtp"
    "net/textproto"
    "net/url"
    "path"
    "strings"
    "time"

    "github.com/rs/xid"

    "github.com/stawi-opportunities/opportunities/pkg/domain"
)

type EmailFallback struct {
    host     string
    port     int
    from     string
    password string
}

func NewEmailFallback(host string, port int, from, password string) *EmailFallback {
    return &EmailFallback{host: host, port: port, from: from, password: password}
}

func (s *EmailFallback) Name() string { return "email" }

func (s *EmailFallback) CanHandle(_ domain.SourceType, applyURL string) bool {
    return strings.HasPrefix(strings.ToLower(strings.TrimSpace(applyURL)), "mailto:")
}
```
**Email submitter**:
- CanHandle: only if URL is a `mailto:` link
- Graceful degradation: if SMTP unconfigured, returns skip/no_smtp

```go
func (s *EmailFallback) Submit(_ context.Context, req SubmitRequest) (SubmitResult, error) {
    if s.host == "" || s.from == "" {
        return SubmitResult{Method: "skipped", SkipReason: "no_smtp"}, nil
    }

    to, err := parseMailtoRecipient(req.ApplyURL)
    if err != nil || to == "" {
        return SubmitResult{Method: "skipped", SkipReason: "empty_recipient"}, nil
    }

    body, err := buildEmailBody(s.from, to, req)
    if err != nil {
        return SubmitResult{}, fmt.Errorf("email: build body: %w", err)
    }

    addr := fmt.Sprintf("%s:%d", s.host, s.port)
    auth := smtp.PlainAuth("", s.from, s.password, s.host)
    if err := smtp.SendMail(addr, auth, s.from, []string{to}, body); err != nil {
        return SubmitResult{}, fmt.Errorf("email: send: %w", err)
    }

    return SubmitResult{Method: "email"}, nil
}
```

**Email submission**:
1. Check SMTP configured
2. Parse `mailto:` recipient
3. Build RFC 5322 email (headers + multipart body + CV attachment)
4. Send via SMTP with PLAIN auth

```go
func parseMailtoRecipient(rawURL string) (string, error) {
    u, err := url.Parse(rawURL)
    if err != nil {
        return "", err
    }
    if !strings.EqualFold(u.Scheme, "mailto") {
        return "", fmt.Errorf("not a mailto URL: %s", u.Scheme)
    }
    decoded, err := url.QueryUnescape(u.Opaque)
    if err != nil {
        decoded = u.Opaque
    }
    addrs, err := mail.ParseAddressList(decoded)
    if err != nil || len(addrs) == 0 {
        return "", fmt.Errorf("invalid mailto recipient: %w", err)
    }
    return addrs[0].Address, nil
}
```

**mailto: parsing**:
- URL scheme must be `mailto:`
- `Opaque` field contains the mailto address(es)
- Percent-decode (e.g., `Hiring%20Team` → `Hiring Team`)
- Parse address list (handles "Name <email@example.com>" format)
- Return first address

```go
func buildEmailBody(from, to string, req SubmitRequest) ([]byte, error) {
    var bodyBuf bytes.Buffer
    w := multipart.NewWriter(&bodyBuf)

    textH := make(textproto.MIMEHeader)
    textH.Set("Content-Type", "text/plain; charset=utf-8")
    textH.Set("Content-Transfer-Encoding", "7bit")
    textPart, err := w.CreatePart(textH)
    if err != nil {
        return nil, err
    }
    plain := fmt.Sprintf(`Dear Hiring Manager,

Please find attached my CV for the position.

Name: %s
Email: %s
Phone: %s
Title: %s

%s

Best regards,
%s
`, req.FullName, req.Email, req.Phone, req.CurrentTitle, req.CoverLetter, req.FullName)
    if _, err := textPart.Write([]byte(plain)); err != nil {
        return nil, err
    }

    if len(req.CVBytes) > 0 {
        attachH := make(textproto.MIMEHeader)
        attachH.Set("Content-Type", "application/octet-stream")
        attachH.Set("Content-Transfer-Encoding", "base64")
        attachH.Set("Content-Disposition",
            fmt.Sprintf("attachment; filename=%q", safeAttachmentName(req.CVFilename)))
        attachPart, err := w.CreatePart(attachH)
        if err != nil {
            return nil, err
        }
        if err := writeBase64Wrapped(attachPart, req.CVBytes); err != nil {
            return nil, err
        }
    }
    if err := w.Close(); err != nil {
        return nil, err
    }

    subject := fmt.Sprintf("Application from %s", req.FullName)

    var msg bytes.Buffer
    fmt.Fprintf(&msg, "From: %s\r\n", from)
    fmt.Fprintf(&msg, "To: %s\r\n", to)
    if req.Email != "" && req.Email != from {
        fmt.Fprintf(&msg, "Reply-To: %s\r\n", req.Email)
    }
    fmt.Fprintf(&msg, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
    fmt.Fprintf(&msg, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
    fmt.Fprintf(&msg, "Message-ID: <%s@autoapply>\r\n", xid.New().String())
    fmt.Fprintf(&msg, "MIME-Version: 1.0\r\n")
    fmt.Fprintf(&msg, "Content-Type: multipart/mixed; boundary=%q\r\n", w.Boundary())
    msg.WriteString("\r\n")
    msg.Write(bodyBuf.Bytes())
    return msg.Bytes(), nil
}
```

**Email building** (RFC 5322):
1. Create multipart writer (mixed: text + attachment)
2. Write plain-text part with candidate info + cover letter
3. If CV available: write base64-encoded attachment
4. Build email headers:
   - **From**: SMTP envelope sender
   - **To**: Recipient
   - **Reply-To**: Candidate email (so replies land with them)
   - **Subject**: Q-encoded if non-ASCII
   - **Date**: RFC 1123 timestamp
   - **Message-ID**: Unique ID for tracking
   - **MIME-Version**: 1.0
   - **Content-Type**: multipart/mixed with boundary
5. Append multipart body

```go
func writeBase64Wrapped(w interface{ Write([]byte) (int, error) }, data []byte) error {
    enc := base64.StdEncoding.EncodeToString(data)
    for len(enc) > 0 {
        n := 76
        if n > len(enc) {
            n = len(enc)
        }
        if _, err := w.Write([]byte(enc[:n] + "\r\n")); err != nil {
            return err
        }
        enc = enc[n:]
    }
    return nil
}
```

**Base64 wrapping** (RFC 2045):
- Encode to base64
- Wrap every 76 characters with CRLF
- Prevents mail servers from munging long lines

```go
func safeAttachmentName(name string) string {
    name = path.Base(name)
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
```

**Attachment name sanitization**:
- Strip path component (base name only)
- Only allow [A-Za-z0-9._-]
- Max 64 characters
- Default to "resume.pdf" if empty

---

This covers all submitter implementations!
