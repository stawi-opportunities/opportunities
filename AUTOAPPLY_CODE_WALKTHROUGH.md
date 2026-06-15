# Complete Autoapply Service Code Walkthrough

## Overview

The autoapply service is an event-driven microservice that automatically submits job applications on behalf of candidates. It receives `AutoApplyIntentV1` events from the matching service and routes them through a tiered submission strategy, handling everything from ATS-specific UI automation to email fallbacks.

---

## 📁 Directory Structure & File Organization

```
apps/autoapply/              # Main service entry point and handlers
├── cmd/main.go              # Service initialization
├── config/config.go         # Configuration with environment variables
└── service/                 # Core handlers and utilities
    ├── handler.go           # Main event handler logic
    ├── cvdownload.go        # CV fetching with SSRF guards
    ├── applications_tracker.go  # Bridge to pkg/applications
    ├── mime.go              # MIME type parsing helpers
    ├── devmode_on/off.go    # Build tag conditional code

pkg/autoapply/              # Core submission library
├── submitter.go            # Submitter interface definition
├── registry.go             # Routes requests to correct submitter
├── browser/client.go       # Headless browser pool (go-rod)
├── ats/                    # ATS-specific submitters
│   ├── greenhouse.go
│   ├── lever.go
│   ├── workday.go
│   ├── smartrecruiters.go
├── sessionsubmitter/       # Browser extension session replay
│   └── submitter.go
├── brightermondaysubmitter/  # BrighterMonday-specific handler
│   └── submitter.go
├── llm_form.go            # Generic LLM-powered form filler
└── email_fallback.go      # Email submission fallback
```

---

## 🔧 apps/autoapply/ - Service Entry Point

### **cmd/main.go**

**Purpose**: Bootstrap the entire autoapply service using the Frame framework.

**Key Responsibilities**:
1. Parse environment configuration (`AUTO_APPLY_*` variables)
2. Initialize Frame service with datastore (PostgreSQL)
3. Set up database auto-migration for `CandidateApplication` table
4. Create all repository instances (ApplicationRepository, MatchRepository)
5. Build the submitter registry in priority order
6. Wire the autoapply handler with all dependencies
7. Start the NATS queue consumer for `AutoApplyIntentV1` events

**Key Components Wired**:
- **PostgreSQL Pool**: Used by repositories for application tracking
- **Browser Pool**: Headless Chromium instances for ATS UI automation (pool size = `BrowserConcurrency`)
- **Auth Manifests**: Loads per-source auth methods from `SourceAuthDir`
- **Session Provider**: Decrypts captured browser sessions using `SessionMasterKey`
- **LLM Client**: Optional OpenAI-compatible endpoint for generic form filling
- **Submitter Registry**: Ordered tiers (BrighterMonday → Session Replay → ATS handlers → LLM → Email → Skip)

**Guardian Checks**:
- Validates that dev-mode flags (`DevAllowInsecureCV`, `DevIntentEndpoint`) only run in `-tags=devmode` builds
- Hard-fails at boot if a production binary is configured with unsafe settings

**Queue Configuration**:
- Topic: `svc.opportunities.autoapply.submit.v1`
- Payload Type: `eventsv1.Envelope[eventsv1.AutoApplyIntentV1]`

---

### **config/config.go**

**Purpose**: Centralized configuration structure with environment variable binding.

**Key Settings** (all optional with sensible defaults):

| Setting | Default | Purpose |
|---------|---------|---------|
| `AUTO_APPLY_ENABLED` | true | Kill switch; when false still consumes & acks but skips submission |
| `AUTO_APPLY_DRY_RUN` | false | Full pipeline without calling submitters (test mode) |
| `AUTO_APPLY_DEV_ALLOW_INSECURE_CV` | false | Bypass HTTPS/private-IP checks (dev only) |
| `AUTO_APPLY_DEV_INTENT_ENDPOINT` | false | Mount POST /dev/intent for manual testing |
| `AUTO_APPLY_QUEUE_URL` | mem://... | NATS/message queue URL |
| `AUTO_APPLY_DAILY_LIMIT_BACKSTOP` | 10 | Consumer-side rate limiter |
| `AUTO_APPLY_SCORE_MIN_BACKSTOP` | 0.0 | Reject matches below this confidence |
| `BROWSER_TIMEOUT_SEC` | 30 | Per-submission timeout |
| `BROWSER_CONCURRENCY` | 2 | Pool size (~150MB per browser) |
| `CV_MAX_BYTES` | 10 MiB | Size guard for CV downloads |
| `CV_DOWNLOAD_TIMEOUT_SEC` | 15 | Per-fetch timeout |
| `INFERENCE_BASE_URL` | (unset) | LLM endpoint; when unset, LLM tier disabled |
| `SESSION_MASTER_KEY` | (unset) | Base64-32-byte encryption key; when unset, session replay disabled |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_FROM`, `SMTP_PASSWORD` | (unset) | Email fallback config |

**Validation** (`Validate()` method):
- Ensures `INFERENCE_MODEL` is set when `INFERENCE_BASE_URL` is provided
- Browser concurrency and timeouts must be >= 1
- CV size caps must be > 0

---

### **service/handler.go**

**Purpose**: Main Frame Queue consumer that orchestrates the entire application submission workflow.

**High-Level Flow**:

```
Event Received (AutoApplyIntentV1)
    ↓
Validate: enabled, required fields, score threshold
    ↓
Fast-path idempotency check: ExistsForCandidate()
    ↓
Atomic slot reservation: Create pending row (unique index prevents race)
    ↓
CV download: Fetch with SSRF guards
    ↓
Build SubmitRequest from intent + CV bytes
    ↓
Dry-run check: If enabled, short-circuit submitter
    ↓
Route through registry: Call submitters in order
    ↓
Finalize: Update status, mark match, emit events
    ↓
Return result (nil = ack, error = redelivery)
```

**Key Interfaces**:

```go
// SubmitterRouter: The Registry (handles routing to correct tier)
type SubmitterRouter interface {
    Submit(ctx context.Context, req autoapply.SubmitRequest) (autoapply.SubmitResult, error)
}

// ApplicationRepo: CRUD operations on candidate_applications table
type ApplicationRepo interface {
    ExistsForCandidate(ctx context.Context, candidateID, canonicalJobID string) (bool, error)
    Create(ctx context.Context, app *domain.CandidateApplication) error
    UpdateStatus(ctx context.Context, id, status, method, externalRef string) error
}

// MatchRepo: Mark upstream matches as applied
type MatchRepo interface {
    MarkApplied(ctx context.Context, id string) error
}

// ApplicationsTracker: Bridge to pkg/applications for user-facing records
type ApplicationsTracker interface {
    Record(ctx context.Context, in TrackerRecord) error
}
```

**Critical Logic**:

1. **Configuration Checks**:
   - If `!cfg.Enabled`: log "disabled by config", ack (no-op)
   - If missing required fields: log "missing required fields", ack
   - If `score < cfg.ScoreMinBackstop`: log "score below backstop", ack

2. **Idempotency Guard (Fast Path)**:
   - Query `ExistsForCandidate(candidateID, canonicalJobID)`
   - If true: already applied in this or prior run → ack silently
   - Prevents queue redelivery from creating duplicates without hitting DB

3. **Atomic Slot Reservation**:
   - Create `CandidateApplication` row with `status=pending`
   - PostgreSQL `UNIQUE PARTIAL INDEX` on `(candidate_id, canonical_job_id) WHERE status='pending'`
   - If unique constraint violated: another worker is in flight → ack "race-dup"
   - If other DB error: transient failure → return error for redelivery

4. **CV Download** (via `h.cv.Fetch()`):
   - Terminal failure if URL unsafe (`ErrCVUnsafeURL`): finalize with `status=failed`, `method=skipped`, `reason=unsafe_cv_url`
   - Non-SSRF errors (network, timeout): soft failure, proceed with `cvBytes=nil`
   - Submitters handle gracefully when CV is unavailable

5. **Dry-Run Mode**:
   - If `h.cfg.DryRun`: finalize with `status=skipped`, `reason=dry_run`, **no submitter call**
   - Useful for full pipeline testing without hitting real ATS systems

6. **Submit Phase**:
   - Call `h.router.Submit()` which routes through the registry
   - Non-nil error = transient, update pending row to `status=failed`, return error for redelivery
   - `SubmitResult` with method/skip_reason = terminal outcome

7. **Finalize** (called for every terminal outcome):
   - Update pending row: `status=[submitted|skipped]`, `method`, `externalRef`
   - If status=submitted && matchID set: call `h.matchRepo.MarkApplied()`
   - Bridge to pkg/applications: if status=submitted && h.tracker != nil, call `tracker.Record()`
   - Emit `ApplicationSubmittedV1` event (detached context, 5s timeout) for analytics
   - If skip_reason in [session_required, session_expired]: emit `SessionRequiredV1` or `SessionExpiredV1` for UI

**Error Classification**:

| Error | Behavior |
|-------|----------|
| DB connection fail | Transient → return error, redelivery |
| Browser crash | Transient → return error, redelivery |
| CAPTCHA wall | Terminal → skip/captcha, ack |
| Form field missing | Terminal → skip/unsupported, ack |
| Unsafe CV URL | Terminal → skip/unsafe_cv_url, ack |
| Session expired | Terminal → skip/session_expired, emit SessionExpiredV1 |

**Helpers**:

- `isUniqueViolation(err)`: Detects PostgreSQL SQLSTATE 23505 (duplicate key)
- `urlHost(raw)`: Extracts hostname for logging
- `emitSessionLifecycleIfApplicable()`: Emits session-related events
- `emitApplicationSubmitted()`: Publishes `ApplicationSubmittedV1` to NATS

---

### **service/cvdownload.go**

**Purpose**: Fetch candidate CV bytes from a signed URL with SSRF guards and size constraints.

**Security Defenses** (SSRF = Server-Side Request Forgery prevention):

1. **HTTPS-Only**:
   - Rejects `http://`, `file://`, `ftp://`
   - Unless `allowInsecure=true` (dev-mode only, blocked by main.go in production)

2. **Private IP Rejection** (via `net.LookupIP()`):
   - Blocks `127.0.0.1`, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`
   - Blocks link-local (`169.254.x.x`), multicast, unspecified
   - Explicitly rejects AWS metadata address `169.254.169.254`

3. **Size Guard**:
   - Default max: 10 MiB (configurable via `CV_MAX_BYTES`)
   - Reads with `io.LimitReader` to avoid memory exhaustion

4. **Timeout**:
   - Default 15 seconds (configurable via `CV_DOWNLOAD_TIMEOUT_SEC`)
   - Per-request via http.Client

**Filename Derivation**:
- Prefers `Content-Disposition: attachment; filename="..."` header
- Falls back to URL path basename
- Sanitizes to `[A-Za-z0-9._-]`, max 64 chars
- Strips both `/` and `\` to prevent path traversal escapes

**Error Handling**:

| Error | Terminal? | Behavior |
|-------|-----------|----------|
| URL unsafe (SSRF) | Yes | Return `ErrCVUnsafeURL` → handler marks app failed |
| DNS fail, network timeout | No | Soft failure → proceed with `cvBytes=nil` |
| File too large | No | Soft failure → proceed without CV |
| HTTP 4xx/5xx | No | Soft failure → proceed without CV |

**API**:

```go
// CVFetcher is the narrow interface (testable)
type CVFetcher interface {
    Fetch(ctx context.Context, rawURL string) (data []byte, filename string, err error)
}

// Factory functions
NewHTTPCVFetcher(timeout, maxBytes)
NewHTTPCVFetcherWithOptions(timeout, maxBytes, allowInsecure)
```

---

### **service/applications_tracker.go**

**Purpose**: Bridge between autoapply's internal `candidate_applications` table and the public-facing `applications` table in pkg/applications.

**Why the Bridge?**
- `candidate_applications`: Internal autoapply record (all attempts, skips, failures)
- `applications`: User-facing record (only successful submissions)
- Candidates see their auto-applied jobs in the main dashboard

**Behavior**:

- Only records **successful** submissions (`status=submitted`)
- Skips and failures stay internal (not user-facing noise)
- Creates single row per (candidate, opportunity, match) tuple
- If row already exists: idempotent → no error, no update

**Implementation** (`PkgApplicationsTracker`):

```go
Record(ctx, TrackerRecord) -> creates applications.Application row with:
  - Status: StatusSubmitted
  - Metadata: {source: "autoapply", method: "session_replay|ats_ui|llm_form|email", ...}
  - MatchID, SourceType, ExternalRef from the submission
```

**Optional Integration**:
- If nil tracker passed to handler: no-op (pre-Phase-5 behavior)
- Production: always wired to enable dashboard visibility

---

### **service/mime.go**

**Purpose**: Minimal MIME type parsing helper.

**Code**:
```go
var mimeParseImpl = mime.ParseMediaType  // Function pointer for test seams
```

**Usage**: Called by `deriveFilename()` in cvdownload.go to extract filename from Content-Disposition headers. Indirection allows tests to inject custom behavior.

---

### **service/devmode_on.go & devmode_off.go**

**Purpose**: Build-tag conditional constant to gate dev-only features.

**devmode_on.go** (with `-tags=devmode`):
```go
const DevModeBuild = true
```

**devmode_off.go** (default):
```go
const DevModeBuild = false
```

**Usage**: `main.go` checks `if !service.DevModeBuild && (cfg.DevAllowInsecureCV || cfg.DevIntentEndpoint)` → fatal, preventing production binaries from running with unsafe flags.

---

## 📚 pkg/autoapply/ - Core Library

### **submitter.go**

**Purpose**: Defines the `Submitter` interface that all handlers implement.

**Interface**:

```go
type Submitter interface {
    // Name: Low-cardinality label for logs/metrics
    // e.g. "greenhouse_ui", "session_replay", "llm_form"
    Name() string
    
    // CanHandle: Returns true if this submitter can process (sourceType, applyURL)
    CanHandle(sourceType domain.SourceType, applyURL string) bool
    
    // Submit: Attempt submission
    // - Non-nil error: transient failure → queue redelivery
    // - SubmitResult with Method="skipped": terminal → ack, don't retry
    Submit(ctx context.Context, req SubmitRequest) (SubmitResult, error)
}
```

**Request/Response Types**:

```go
type SubmitRequest struct {
    SourceType    domain.SourceType  // greenhouse, lever, workday, etc.
    ApplyURL      string             // Where to apply
    CandidateID   string             // For logging/tracking
    FullName      string
    Email         string
    Phone         string
    Location      string
    CurrentTitle  string
    Skills        string
    CoverLetter   string
    CVBytes       []byte             // nil if unavailable; submitters handle gracefully
    CVFilename    string             // e.g. "resume.pdf"
}

type SubmitResult struct {
    Method       string  // "ats_ui", "session_replay", "llm_form", "email", "skipped"
    ExternalRef  string  // Optional ATS application ID
    SkipReason   string  // When Method=="skipped": "captcha", "unsupported", "session_required", etc.
}
```

---

### **registry.go**

**Purpose**: Routes each submission request to the first matching submitter.

**Design Pattern**: Chain of Responsibility (ordered list of handlers)

```go
type Registry struct {
    tiers []Submitter
}

// Submit: Try each tier in order; return first match
func (r *Registry) Submit(ctx context.Context, req SubmitRequest) (SubmitResult, error) {
    for _, s := range r.tiers {
        if !s.CanHandle(req.SourceType, req.ApplyURL) {
            continue
        }
        return s.Submit(ctx, req)
    }
    // No tier matched
    return SubmitResult{Method: "skipped", SkipReason: "no_submitter"}, nil
}
```

**Typical Registry Order** (as built in main.go):

1. **BrighterMonday Session Replay** (for brightermonday sources with captured session)
2. **Generic Session Replay** (for any source with extension manifest + captured session)
3. **Greenhouse UI** (Greenhouse ATS)
4. **Lever UI** (Lever ATS)
5. **Workday UI** (Workday ATS)
6. **SmartRecruiters UI** (SmartRecruiters ATS)
7. **LLM Form** (Generic catch-all, if LLM endpoint configured)
8. **Email Fallback** (mailto: URLs or last resort)
9. **Implicit Skip** (if no tier matched)

---

### **browser/client.go**

**Purpose**: Headless browser automation pool using go-rod (WebDriver protocol).

**Architecture**:

```
Pool (fixed-size slot channel)
  ├── instance 0: one browser process + mutex
  ├── instance 1: one browser process + mutex
  └── ... (size configurable via BrowserConcurrency)
```

**Design Rationale**:
- Slot-based pool prevents unbounded browser launches
- Per-instance mutex ensures one submission uses one browser exclusively
- Hung page doesn't block siblings (per-instance locking)
- ~150 MB RSS per browser → pool size is memory/throughput tradeoff

**Key Methods**:

```go
// acquire: Pull an instance from the pool (blocks on ctx)
// Must be paired with release()
acquire(ctx context.Context) (*instance, error)

// release: Return instance to the pool (if not closed)
release(inst *instance)

// FillAndSubmit: Main API
// 1. Navigate to URL
// 2. Fill text fields by CSS selector
// 3. Upload file to file input
// 4. Click submit button
// 5. Wait for confirmation (selector or text)
FillAndSubmit(ctx context.Context, opts SubmitOptions) error

// GetHTML: Fetch fully-rendered HTML (used by LLM form filler)
GetHTML(ctx context.Context, url string) (string, error)

// Close: Shutdown all browsers (idempotent)
Close()
```

**Error Signals** (terminal):
- `ErrCAPTCHA`: CAPTCHA iframe detected (hcaptcha, recaptcha, cf-turnstile)
- `ErrElementNotFound`: Required selector missing after timeout
- `ErrSubmitNotConfirmed`: Submit clicked but no confirmation signal detected

**Other Errors** (transient):
- Page creation failure, navigation timeout, network errors → return error for redelivery

**Health Check**:
- `instance.ensure()` calls `browser.Version()` to detect dead processes
- If health check fails: close browser and relaunch
- Fresh launch uses `launcher.New()` with sensible defaults:
  - `disable-gpu`, `no-sandbox`, `disable-dev-shm-usage` (Docker-friendly)
  - `disable-blink-features=AutomationControlled` (defeat detection)
  - `Leakless(true)` (resource cleanup)

**CAPTCHA Detection**:
```go
hasCAPTCHA(page) bool:
    Checks HTML for "hcaptcha", "recaptcha", "cf-turnstile" (case-insensitive)
```

**Confirmation Verification**:
- `ConfirmSel`: CSS selector that must appear after submit
- `ConfirmText`: Text (case-insensitive substring) that must appear in HTML
- If neither set: legacy "trust the click" behavior (submitters should migrate to explicit verification)

**Temp File Handling** (for CV upload):
- `writeTempFile()`: Sanitizes filename, creates temp file with pattern `autoapply_*_<sanitized>`
- Sanitization strips path separators so a crafted filename can't escape `/tmp`
- Deleted after file upload via deferred `os.Remove()`

---

### **ats/greenhouse.go**

**Purpose**: ATS-specific handler for Greenhouse job boards.

**Greenhouse URLs**:
- `boards.greenhouse.io/<company>/jobs/<id>`
- Detection: check for `boards.greenhouse.io`, `gh_jid=`, or `greenhouse.io/jobs` in URL

**Form Fields** (CSS selectors):
- Name: `#first_name`, `#last_name` (or `#job_application_first_name`, etc.)
- Email: `#email` (or `#job_application_email`)
- Phone: `#phone` (or `#job_application_phone`)
- Cover Letter: `#cover_letter_text` (optional, if req.CoverLetter provided)
- Resume: `#resume` (or `#job_application_resume`)
- Submit Button: `[data-submit='true']` or generic submit

**Confirmation**: Looks for "thank you for applying" text in page after submit

**Error Mapping**:
- CAPTCHA → skip/captcha
- Element not found → skip/unsupported
- Submit not confirmed → skip/not_confirmed
- Other errors → transient (redelivery)

---

### **ats/lever.go**

**Purpose**: ATS handler for Lever job boards.

**Lever URLs**:
- `jobs.lever.co/<company>/...`

**Form Fields** (attribute selectors):
- Name: `[name='name']`
- Email: `[name='email']`
- Phone: `[name='phone']`
- Comments (cover letter): `[name='comments']`
- Resume: `[name='resume']`
- Submit: `[data-qa='btn-submit-application']` or generic submit

**Confirmation**: "application submitted" text in page

**Note**: Lever has an `[name='org']` field (candidate's current company) that we don't populate yet.

---

### **ats/workday.go**

**Purpose**: ATS handler for Workday application forms.

**Workday URLs**:
- `myworkdayjobs.com/...` or `myworkday.com/...`

**Key Difference**: Workday uses `data-automation-id` attributes (not traditional form names).

**Form Fields**:
- First Name: `[data-automation-id='firstName']`
- Last Name: `[data-automation-id='lastName']`
- Email: `[data-automation-id='email']`
- Phone: `[data-automation-id='phone-number']`
- Resume: `[data-automation-id='file-upload-input-ref']`
- Submit: `[data-automation-id='bottom-navigation-next-button']` or `saveAndContinueButton`

**Multi-Step Flow**: Workday is multi-step (Save & Continue → next page).

**Confirmation**: Looks for progress bar selectors:
- `[data-automation-id='progressBar']`
- `[data-automation-id='applicationProgress']`

---

### **ats/smartrecruiters.go**

**Purpose**: ATS handler for SmartRecruiters job boards.

**SmartRecruiters URLs**:
- `jobs.smartrecruiters.com/<company>/...`

**Form Fields** (name attributes):
- First Name: `[name='firstName']`
- Last Name: `[name='lastName']`
- Email: `[name='email']`
- Phone: `[name='phoneNumber']`
- Resume: `input[type='file']`
- Submit: `[data-test-id='apply-button-bottom']` or generic submit

**Confirmation**: "application sent" text

---

### **sessionsubmitter/submitter.go**

**Purpose**: Replay HTTP-form applications using sessions captured by the Stawi browser extension.

**Prerequisites**:
- Source must have extension-based auth manifest (`auth_method: extension`)
- Apply flow must be HTTP form (`apply_flow.type: http_form`)
- Candidate must have a valid captured session

**Session Lifecycle**:

```
1. Lookup manifest for source type
   ├─ If not found or auth_method != extension: refuse to handle
   └─ If apply_flow.type != http_form: refuse to handle

2. Load session via SessionProvider
   ├─ If ErrSessionRequired: return skip/session_required
   └─ If ErrSessionExpired: return skip/session_expired

3. Build HTTP client with pre-seeded cookies
   └─ Copy User-Agent, Accept-Language from captured headers

4. GET apply URL
   ├─ Parse for CSRF token if declared in manifest
   ├─ Detect logged-out (3xx to /login, 401): revoke session & return skip/session_expired
   └─ Extract form HTML

5. Build multipart POST body
   ├─ Map manifest field names to SubmitRequest values
   └─ Bail with skip/form_changed if mapping fails

6. POST to form's action attribute
   ├─ Include CSRF token in header if declared
   ├─ Set Referer, Origin from captured session
   └─ Check response status

7. Classify outcome
   ├─ 2xx / 302: success
   ├─ 4xx: form_changed (BM rejected)
   └─ Session errors: revoke & signal
```

**Session Expiry Detection**:
- 3xx redirect to `/login` → session expired
- 401 Unauthorized → session expired
- Triggers `Sessions.Revoke()` so next attempt → session_required

**CSRF Handling**:
- If manifest declares a CSRF pattern: extract from HTML or fetch from dedicated endpoint
- Include in POST header as declared in manifest
- Example: `_token` hidden input → re-POST as `_token` header

---

### **brightermondaysubmitter/submitter.go**

**Purpose**: Specialized handler for BrighterMonday's "Easy Apply" flow.

**BrighterMonday Specifics**:
- Classic Laravel form (`/account/customer/enquiries/<listing_id>/store-enquiry`)
- Form fields are mostly hidden and pre-populated from the candidate's BM profile
- Only cover letter (`description` textarea) is candidate-supplied per application

**Strategy** (Profile-Conservative):
1. Scrape live form for every hidden field (`<input type="hidden">`)
2. Extract selected `<select>` option values
3. Replay them verbatim (so new fields added by BM flow through automatically)
4. Override `description` with candidate's cover letter from intent

**Flow**:
1. Load candidate's BM session
2. GET listing page
3. Parse `<form id="enquiry-form">`
4. Extract all hidden inputs and select values
5. Override `description` with cover letter
6. POST multipart to form action
7. 2xx/302 = success; 4xx = form_changed

**Logged-Out Detection**:
- Check for `Location: /login` redirect or 401
- If detected: revoke session & return skip/session_expired

---

### **llm_form.go**

**Purpose**: Generic form filler using LLM to infer field mappings.

**Tier Placement**: Catch-all (after all ATS-specific and session-replay tiers).

**Workflow**:

```
1. Fetch apply page HTML via browser.GetHTML()
2. Extract <form> elements
3. Score each form: inputs*2 + textareas*3 + selects*2 + keywords
   ├─ Boost: "apply", "application", "resume", "cv", "first name", etc.
   └─ Penalize: "search", "subscribe", "login", etc.
4. Select highest-scoring form
5. Strip <script> and <style> tags
6. Truncate to 4000 runes for LLM token budget
7. Send to LLM:
   System: "You are a job form assistant. Return JSON mapping CSS selectors to values."
   User: "[form HTML]\n[candidate profile]"
8. Parse LLM response as JSON
   ├─ Coerce all values to strings (handle booleans, numbers)
   └─ If empty: skip/llm_no_fields
9. Call browser.FillAndSubmit() with LLM-inferred field map
10. Return result
```

**Form Scoring** (`scoreForm()`):
```
score = 0
score += <input> count * 2
score += <textarea> count * 3
score += <select> count * 2
for each keyword in [apply, application, resume, ...]:
    score += 5 if keyword found
for each anti-keyword in [search, subscribe, ...]:
    score -= 4
```

**Field Parsing** (`parseFieldMap()`):
- Strips markdown fences (in case LLM wraps response in ` ```json ... ``` `)
- Unmarshals JSON, coerces all values to strings
- Handles LLM quirks (returning `{"name": 5}` instead of `{"name": "John"}`)

**Error Handling**:
- CAPTCHA → skip/captcha
- Element not found → skip/unsupported
- LLM returns no fields → skip/llm_no_fields
- Other errors → transient

**CanHandle** Returns `true` if:
- URL is non-empty (catch-all)
- LLM endpoint is configured (`llm != nil`)

---

### **email_fallback.go**

**Purpose**: Last-resort email submission for `mailto:` URLs.

**Tier Placement**: After all browser-based tiers.

**Graceful Degradation**:
- If SMTP unconfigured (`host == ""` or `from == ""`): return skip/no_smtp (not an error)
- Allows service to run without email infrastructure

**Workflow**:

```
1. Parse mailto: URL to extract recipient email
2. Validate recipient with net/mail.ParseAddressList()
3. Build RFC 5322 multipart message:
   ├─ Plain-text part with candidate profile (name, email, phone, title, cover letter)
   └─ Base64-encoded attachment with CV bytes (if present)
4. Set headers:
   - From: SMTP From address
   - To: Extracted recipient
   - Reply-To: Candidate email (so replies come back to them)
   - Subject: "Application from [Name]"
   - MIME-Version, Content-Type, Message-ID
5. Send via SMTP (port 587, PLAIN auth)
6. Return Method="email" on success
```

**mailto: URL Examples**:
- `mailto:hiring@company.com`
- `mailto:team@company.com?subject=Application`
- `mailto:Hiring%20Team%3Ehr@company.com` (percent-encoded, decoded before parsing)

**Email Body**:
```
Dear Hiring Manager,

Please find attached my CV for the position.

Name: [Full Name]
Email: [Email]
Phone: [Phone]
Title: [Current Title]

[Cover Letter]

Best regards,
[Full Name]
```

**Attachment Handling**:
- Filename sanitized (only `[A-Za-z0-9._-]`, max 64 chars)
- Base64 encoded with CRLF every 76 chars (RFC 2045)
- Content-Disposition header with safe filename

**Error Classification**:
- Recipient parsing fail → skip/empty_recipient
- SMTP config missing → skip/no_smtp
- SMTP send fail → transient (return error for redelivery)

---

## 🔄 Data Flow Example

### Scenario: Automatic Application via Greenhouse

```
1. Matching Service Emits Event
   AutoApplyIntentV1 {
       CandidateID: "cand_123",
       CanonicalJobID: "job_456",
       ApplyURL: "https://boards.greenhouse.io/acme/jobs/789",
       SourceType: "greenhouse",
       Score: 0.92,
       FullName: "Jane Doe",
       Email: "jane@example.com",
       Phone: "+1234567890",
       CVUrl: "https://signed-url-to-cv.s3.amazonaws.com/jane_doe.pdf"
   }

2. Autoapply Handler Receives on Queue
   handler.Handle(ctx, env) {
       // Unmarshal, validate, log
       // Check enabled, score > backstop
       // Fast-path: ExistsForCandidate(cand_123, job_456)? No, continue.
   }

3. Atomic Slot Reservation
   appRepo.Create(ctx, CandidateApplication{
       CandidateID: "cand_123",
       CanonicalJobID: "job_456",
       Status: "pending",
       ApplyURL: "...",
       Method: "auto"
   }) // Unique partial index prevents duplicates

4. CV Download
   cvDownload.Fetch(ctx, "https://signed-url...") {
       // Check HTTPS, not private IP
       // GET, check size < 10 MiB
       // Return (bytes, "jane_doe.pdf", nil)
   }

5. Build SubmitRequest
   req := SubmitRequest{
       SourceType: "greenhouse",
       ApplyURL: "...",
       FullName: "Jane Doe",
       Email: "jane@example.com",
       CVBytes: [...],
       CVFilename: "jane_doe.pdf"
   }

6. Registry Routing
   registry.Submit(ctx, req) {
       // BrighterMonday: CanHandle? No (sourceType != brightermonday)
       // SessionReplay: CanHandle? No (no manifest for greenhouse)
       // GreenhouseSubmitter: CanHandle? Yes (sourceType == greenhouse)
       // return greenhouseSubmitter.Submit(ctx, req)
   }

7. Greenhouse Submitter
   Submit(ctx, req) {
       // Parse name → "Jane", "Doe"
       // Build textFields map: {"#first_name": "Jane", "#email": "jane@example.com", ...}
       // Call browser.FillAndSubmit(opts) {
       //     Navigate to boards.greenhouse.io/acme/jobs/789
       //     Fill #first_name = "Jane", #last_name = "Doe", #email, #phone
       //     Upload CV to #resume
       //     Click submit button
       //     Wait for "thank you for applying" text
       //     Return nil (success)
       // }
       // Return SubmitResult{Method: "ats_ui"}
   }

8. Finalize
   handler.finalise(ctx, pending, intent, result={Method: "ats_ui"}) {
       // Update pending row: status="submitted", method="ats_ui"
       // markRepo.MarkApplied(intent.MatchID)
       // tracker.Record(TrackerRecord{...}) -> pkg/applications row
       // Emit ApplicationSubmittedV1 event
       // Return nil (ack message)
   }

9. Event Emission
   ApplicationSubmittedV1 {
       CandidateID: "cand_123",
       CanonicalJobID: "job_456",
       Method: "ats_ui",
       Status: "submitted",
       SubmittedAt: now(),
       ApplicationID: "pending row ID"
   }
   // Consumed by:
   // - pkg/eventlog writer (Iceberg/Parquet analytics)
   // - Dashboard refresh
   // - Notification service (email candidate)
```

---

## 🧪 Testing Strategy

### Unit Tests (tests/integration/ or *_test.go):

1. **handler_test.go**: Mock router, app repo, match repo; verify idempotency, status transitions
2. **cvdownload_test.go**: Test SSRF guards, filename derivation, size limits
3. **registry_test.go**: Verify tier ordering and fallthrough
4. **ats/*_test.go**: Mock browser client, verify field mapping
5. **browser/client_test.go**: Mock go-rod, test pool behavior, timeout handling
6. **llm_form_test.go**: Mock LLM client, test form scoring and field parsing
7. **email_fallback_test.go**: Test email building, mailto parsing

### Integration Tests (tests/integration/):
- Spin up real PostgreSQL, NATS, Manticore containers
- Test full pipeline: event → DB → submission → event emission

---

## 🚀 Deployment Checklist

1. **Environment Variables**:
   ```bash
   AUTO_APPLY_ENABLED=true
   BROWSER_CONCURRENCY=2
   CV_MAX_BYTES=10485760
   INFERENCE_BASE_URL=http://llm-service:8000/v1  # optional
   SESSION_MASTER_KEY=<base64-32-bytes>  # optional
   SOURCE_AUTH_DIR=/etc/source-auth
   SMTP_HOST=mail.company.com  # optional
   SMTP_PORT=587
   SMTP_FROM=autoapply@company.com
   SMTP_PASSWORD=<secret>
   ```

2. **Database**:
   - PostgreSQL with `candidate_applications` table
   - Unique partial index on `(candidate_id, canonical_job_id) WHERE status='pending'`

3. **Browser Requirements** (if running headless):
   - Chromium/Chrome binary in PATH or set `$CHROME_BIN`
   - `xvfb-run` if no display (or use `--headless`)
   - Docker: use sandbox-compatible flags in browser launcher

4. **Security**:
   - `SESSION_MASTER_KEY` rotated periodically (new key ID in SESSION_MASTER_KEY_ID)
   - HTTPS enforced for CV downloads
   - SMTP credentials stored in secrets manager

5. **Monitoring**:
   - Metrics: `autoapply_attempt_total`, `autoapply_submit_duration_seconds`, `autoapply_transient_error_total`
   - Logs: Watch for high `captcha`, `unsupported`, `session_required` rates
   - Alerts: Transient error spikes, browser pool exhaustion

---

## Summary

The autoapply service is a well-engineered, **security-first** job application automation platform. Every component prioritizes:

- **Idempotency**: Unique partial indexes + pending row atomicity prevent duplicates
- **Security**: SSRF guards, HTTPS-only CV downloads, session encryption
- **Graceful Degradation**: Missing LLM endpoint, SMTP, or session key disables that tier, not the whole service
- **Observable Errors**: Low-cardinality skip reasons (captcha, unsupported, session_required) enable metrics
- **Transient vs Terminal**: Clear classification ensures queue redelivery for infrastructure failures but respects page changes
- **Extensibility**: New ATS systems added by implementing `Submitter` interface; registry automatically picks up

The tiered architecture (ATS-specific → Session Replay → Generic → Email → Skip) balances reliability (known systems first) with flexibility (LLM fallback for unknown forms).
