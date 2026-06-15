# Complete Line-by-Line Code Explanations - Autoapply Service

---

## 1. apps/autoapply/cmd/main.go

### Imports Section (Lines 1-28)

```go
package main
```
**Explanation**: This is the executable entry point. When compiled, Go will look for `func main()` in this package.

```go
import (
    "bytes"                    // For buffered I/O operations (used in OpenAI client request building)
    "context"                  // Go's context package for cancellation and timeouts
    "encoding/json"            // JSON marshaling/unmarshaling
    "fmt"                      // Formatted string printing
    "io"                       // I/O interfaces (Read, Write, etc.)
    "net/http"                 // HTTP client/server
    "os"                       // Operating system interaction
    "time"                     // Time operations and durations
```

```go
    "github.com/pitabwire/frame"
    fconfig "github.com/pitabwire/frame/config"
    "github.com/pitabwire/frame/datastore"
    "github.com/pitabwire/util"
```
These are from the Frame framework (internal microservice library):
- **frame**: Main service orchestration (NATS queue, HTTP server, lifecycle)
- **fconfig**: Configuration management (FromEnv generic function)
- **datastore**: Database connection pooling
- **util**: Logging utilities (Log, WithError, etc.)

```go
    autoapplyconfig "github.com/stawi-opportunities/opportunities/apps/autoapply/config"
    "github.com/stawi-opportunities/opportunities/apps/autoapply/service"
```
Autoapply-specific packages:
- **config**: Holds `AutoApplyConfig` struct with environment variable bindings
- **service**: Contains handler, CV downloader, tracker

```go
    "github.com/stawi-opportunities/opportunities/pkg/applications"
    "github.com/stawi-opportunities/opportunities/pkg/authmanifest"
    "github.com/stawi-opportunities/opportunities/pkg/authsession"
    "github.com/stawi-opportunities/opportunities/pkg/autoapply"
    "github.com/stawi-opportunities/opportunities/pkg/autoapply/ats"
    "github.com/stawi-opportunities/opportunities/pkg/autoapply/brightermondaysubmitter"
    "github.com/stawi-opportunities/opportunities/pkg/autoapply/browser"
    "github.com/stawi-opportunities/opportunities/pkg/autoapply/sessionsubmitter"
    "github.com/stawi-opportunities/opportunities/pkg/domain"
    eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
    "github.com/stawi-opportunities/opportunities/pkg/repository"
    "github.com/stawi-opportunities/opportunities/pkg/telemetry"
```
Core library packages:
- **applications**: User-facing application tracking
- **authmanifest**: Per-source auth method definitions (extension, oauth, etc.)
- **authsession**: Session encryption/decryption for captured cookies
- **autoapply**: Core submitter interface and implementations
- **ats**: ATS-specific submitters (Greenhouse, Lever, etc.)
- **domain**: Core types (CandidateApplication, SourceType, etc.)
- **eventsv1**: Event definitions (AutoApplyIntentV1, ApplicationSubmittedV1, etc.)
- **repository**: Database ORM repositories
- **telemetry**: Metrics recording

---

## main() Function - Configuration & Validation (Lines 30-50)

```go
func main() {
    ctx := context.Background()
```
Creates a root context with no timeout. This is the parent context for the entire service lifetime.

```go
    cfg, err := fconfig.FromEnv[autoapplyconfig.AutoApplyConfig]()
    if err != nil {
        util.Log(ctx).WithError(err).Fatal("autoapply: config parse failed")
    }
```
- **fconfig.FromEnv**: Generic function that uses Go reflection to read struct tags and populate from environment variables
- Example: field `Enabled bool` with tag `env:"AUTO_APPLY_ENABLED" envDefault:"true"` reads `$AUTO_APPLY_ENABLED` or defaults to true
- **Fatal**: Logs and exits the program with code 1

```go
    if err := cfg.Validate(); err != nil {
        util.Log(ctx).WithError(err).Fatal("autoapply: config invalid")
    }
```
Calls `AutoApplyConfig.Validate()` method (defined in config/config.go) to check:
- If INFERENCE_BASE_URL is set, INFERENCE_MODEL must also be set
- Browser concurrency >= 1
- Timeouts >= 1
- CV max bytes > 0

---

## Production Safety Check (Lines 52-58)

```go
    if !service.DevModeBuild && (cfg.DevAllowInsecureCV || cfg.DevIntentEndpoint) {
        util.Log(ctx).Fatal(
            "autoapply: AUTO_APPLY_DEV_ALLOW_INSECURE_CV / AUTO_APPLY_DEV_INTENT_ENDPOINT " +
                "are only honoured when the binary is built with -tags=devmode")
    }
```
**Critical Security Check**:
- **!service.DevModeBuild**: Checks if binary was compiled with `-tags=devmode` (if false = production binary)
- If production binary AND dev flags are set: FATAL
- This prevents accidental deployment of a production binary with SSRF guards disabled
- The comment explains: these flags bypass security controls that should never exist in production

---

## Frame Service Initialization (Lines 60-65)

```go
    opts := []frame.Option{
        frame.WithConfig(&cfg),
        frame.WithDatastore(),
    }
    ctx, svc := frame.NewServiceWithContext(ctx, opts...)
    log := util.Log(ctx)
```
- **frame.WithConfig**: Tells Frame to use our AutoApplyConfig
- **frame.WithDatastore**: Tells Frame to initialize database connection pooling
- **frame.NewServiceWithContext**: Creates the Frame service with the given options
  - Returns: new context (with Frame logger injected) and the service handle
- **util.Log(ctx)**: Gets the logger from the context (now includes Frame's logger)

---

## Telemetry & Database Setup (Lines 67-83)

```go
    if err := telemetry.Init(); err != nil {
        log.WithError(err).Warn("autoapply: telemetry init failed")
    }
```
Initializes metrics/observability. Logs warning if it fails but continues (non-fatal).

```go
    pool := svc.DatastoreManager().GetPool(ctx, datastore.DefaultPoolName)
    if pool == nil {
        log.Fatal("autoapply: no datastore pool available — set DATABASE_URL")
    }
```
- Gets the connection pool initialized by `frame.WithDatastore()`
- Returns nil if DATABASE_URL environment variable was not set
- Fatal if pool is nil (can't proceed without database)

```go
    gdb := pool.DB(ctx, false)
    if gdb == nil {
        log.Fatal("autoapply: datastore pool returned a nil DB — DATABASE_URL likely unset or unreachable")
    }
```
- **pool.DB(ctx, false)**: Opens a GORM database handle from the pool
  - Second param `false` means "don't retry if connection fails"
- Returns nil if database is unreachable
- Fatal if nil (database must be available)

```go
    if err := gdb.AutoMigrate(&domain.CandidateApplication{}); err != nil {
        log.WithError(err).Warn("autoapply: auto-migrate candidate_applications failed")
    }
```
- **AutoMigrate**: GORM function that creates/updates the `candidate_applications` table schema
- Reads struct tags on `domain.CandidateApplication` (primary key, unique constraints, etc.)
- Logs warning if migration fails but continues (table might already exist)

```go
    appRepo := repository.NewApplicationRepository(pool.DB)
    matchRepo := repository.NewMatchRepository(pool.DB)
```
Creates ORM repositories:
- **appRepo**: CRUD operations on `candidate_applications` table
- **matchRepo**: Updates `matches` table when application succeeds

---

## Browser Pool Creation (Lines 85-94)

```go
    browserClient := browser.NewPool(
        cfg.BrowserConcurrency,           // Default 2 — size of the pool
        time.Duration(cfg.BrowserTimeoutSec)*time.Second,  // Default 30s timeout per submission
        cfg.BrowserUserAgent,             // Custom User-Agent (optional override)
    )
    defer browserClient.Close()
```
- Creates a fixed-size pool of headless Chromium instances
- **defer**: Ensures browsers are closed when main() exits (cleanup)
- Pool size determines memory usage (~150MB per browser) and concurrency

---

## Auth Manifests & Session Provider Setup (Lines 96-131)

```go
    authManifests, err := authmanifest.LoadFromDir(cfg.SourceAuthDir)
    if err != nil {
        log.WithError(err).Fatal("autoapply: source-auth manifest load failed")
    }
    log.WithField("source_auth_count", len(authManifests.Known())).
        WithField("source_auth_dir", cfg.SourceAuthDir).
        Info("autoapply: source-auth manifests loaded")
```
- **LoadFromDir**: Loads YAML files (per-source manifests) from `$SOURCE_AUTH_DIR` (default `/etc/source-auth`)
- Each manifest defines: auth method (extension, oauth, etc.), apply URL pattern, form field mappings
- Logs the number of loaded manifests and directory path for debugging

```go
    var sessionProvider authsession.SessionProvider
    if cfg.SessionMasterKey != "" {
        wrapper, werr := authsession.NewLocalWrapperFromBase64(cfg.SessionMasterKey, cfg.SessionMasterKeyID)
        if werr != nil {
            log.WithError(werr).Fatal("autoapply: session master key invalid")
        }
```
- **SessionProvider**: Interface for loading/decrypting captured browser sessions
- **if cfg.SessionMasterKey != ""**: Optional — only when session replay is enabled
- **NewLocalWrapperFromBase64**: Decodes the base64-encoded 32-byte encryption key
  - KEY_ID is stored with each encrypted session to support key rotation
- Fatal if key is invalid (can't decrypt sessions)

```go
        if err := gdb.AutoMigrate(&domain.CandidateSession{}); err != nil {
            log.WithError(err).Warn("autoapply: auto-migrate candidate_sessions failed")
        }
        sessionRepo := repository.NewCandidateSessionRepository(pool.DB)
        sessionProvider = authsession.NewStore(sessionRepo, wrapper)
        log.Info("autoapply: session-replay submitter enabled")
    } else {
        log.Warn("autoapply: SESSION_MASTER_KEY unset — session-replay disabled")
    }
```
- Creates the `candidate_sessions` table if it doesn't exist
- **authsession.NewStore**: Wraps the repo + encryption wrapper into a SessionProvider
- Logs either "enabled" or "disabled" depending on configuration

---

## Submitter Registry Construction (Lines 133-165)

```go
    tiers := []autoapply.Submitter{}
    if sessionProvider != nil {
```
Creates an ordered list of Submitters. Order matters—first match wins.

```go
        tiers = append(tiers, brightermondaysubmitter.New(brightermondaysubmitter.Config{
            Sessions:    sessionProvider,
            HTTPTimeout: 30 * time.Second,
        }))
        tiers = append(tiers, sessionsubmitter.New(sessionsubmitter.Config{
            Manifests:   authManifests,
            Sessions:    sessionProvider,
            HTTPTimeout: 30 * time.Second,
        }))
    }
```
- **BrighterMonday Tier (Tier 0)**: Specialized session-replay for brightermonday.co.ke
- **Generic Session Replay Tier (Tier 1)**: Replays HTTP forms using captured sessions for any source with a manifest
- Only added if sessionProvider is non-nil (SESSION_MASTER_KEY is set)
- BM tier comes first because it has special URL parsing logic

```go
    tiers = append(tiers,
        ats.NewGreenhouseSubmitter(browserClient),
        ats.NewLeverSubmitter(browserClient),
        ats.NewWorkdaySubmitter(browserClient),
        ats.NewSmartRecruitersSubmitter(browserClient),
    )
```
- **Tier 2-5**: ATS UI handlers
- Each receives the shared `browserClient` pool for headless automation

```go
    if cfg.InferenceBaseURL != "" {
        llm := newOpenAIClient(cfg.InferenceBaseURL, cfg.InferenceAPIKey, cfg.InferenceModel)
        tiers = append(tiers, autoapply.NewLLMFormSubmitter(browserClient, llm))
    }
```
- **Tier 6 (conditional)**: LLM-powered generic form filler
- Only added if INFERENCE_BASE_URL is set (i.e., LLM service is available)

```go
    tiers = append(tiers,
        autoapply.NewEmailFallback(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPFrom, cfg.SMTPPassword),
    )
```
- **Tier 7 (final)**: Email fallback
- Gracefully handles nil SMTP config (returns skip/no_smtp instead of crashing)

```go
    registry := autoapply.NewRegistry(tiers...)
```
Creates the registry with all tiers in order. When a request comes in, it tries each tier until one returns a result.

---

## CV Downloader & Applications Tracker Setup (Lines 167-199)

```go
    cvFetcher := service.NewHTTPCVFetcherWithOptions(
        time.Duration(cfg.CVDownloadTimeoutSec)*time.Second,
        cfg.CVMaxBytes,
        cfg.DevAllowInsecureCV,  // true only in devmode builds with flag set
    )
```
Creates the CV downloader with SSRF guards. Third param allows bypassing HTTPS check in local dev.

```go
    sqlDB, err := gdb.DB()
    if err != nil {
        log.WithError(err).Fatal("autoapply: open *sql.DB for applications tracker")
    }
    tracker := service.NewPkgApplicationsTracker(applications.NewStore(sqlDB))
```
- **gdb.DB()**: Extracts the underlying `*sql.DB` from the GORM connection (same pool)
- Creates the tracker to bridge autoapply rows to the user-facing applications table
- Fatal if this fails (applications bridge is required for v1 contract)

---

## Handler Wiring (Lines 201-219)

```go
    queueHandler := service.NewAutoApplyHandler(service.HandlerDeps{
        Svc:       svc,        // Frame service handle
        Router:    registry,   // The submitter registry
        AppRepo:   appRepo,    // Application CRUD repository
        MatchRepo: matchRepo,  // Match update repository
        Tracker:   tracker,    // Applications bridge
        CV:        cvFetcher,  // CV downloader
        Config: service.Config{
            Enabled:            cfg.Enabled,
            DryRun:             cfg.DryRun,
            DailyLimitBackstop: cfg.DailyLimitBackstop,
            ScoreMinBackstop:   cfg.ScoreMinBackstop,
        },
    })
```
Creates the autoapply handler with all dependencies using named fields (safe for future additions).

```go
    svc.Init(ctx,
        frame.WithRegisterPublisher(eventsv1.SubjectAutoApplySubmit, cfg.AutoApplyQueueURL),
        frame.WithRegisterSubscriber(eventsv1.SubjectAutoApplySubmit, cfg.AutoApplyQueueURL, queueHandler),
    )
```
- **RegisterPublisher**: Allows the service to emit events (when marking apps as submitted)
- **RegisterSubscriber**: Listens to `AutoApplyIntentV1` events on the configured queue URL
  - Calls `queueHandler.Handle()` for each message

---

## HTTP Server Setup (Lines 221-232)

```go
    mux := http.NewServeMux()
    mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
        w.WriteHeader(http.StatusOK)
        _, _ = w.Write([]byte(`{"status":"ok"}`))
    })
```
Creates HTTP mux with healthcheck endpoint. Kubernetes uses this to verify the service is alive.

```go
    if cfg.DevIntentEndpoint && service.DevModeBuild {
        mux.Handle("POST /dev/intent", devIntentHandler(svc, cfg.AutoApplyQueueURL))
        log.Warn("autoapply: POST /dev/intent enabled — devmode build only")
    }
```
- Conditionally mounts `POST /dev/intent` endpoint for manual testing
- Only if: flag is enabled AND binary was built with `-tags=devmode`
- Prevents production binaries from having this endpoint

```go
    svc.Init(ctx, frame.WithHTTPHandler(mux))
```
Tells Frame to use our mux for HTTP requests.

---

## Service Start & Shutdown (Lines 234-238)

```go
    log.Info("autoapply: service starting")
    if err := svc.Run(ctx, ""); err != nil {
        log.WithError(err).Error("autoapply: service run failed")
        os.Exit(1)
    }
```
- **svc.Run()**: Blocks forever, listening to NATS queue and HTTP requests
- If it returns (error), log and exit with code 1
- Second param `""` means use default graceful shutdown timeout

---

## openAIClient Implementation (Lines 287-360)

```go
type openAIClient struct {
    baseURL string        // e.g., "http://llm-service:8000/v1"
    apiKey  string        // Optional auth header
    model   string        // e.g., "gpt-3.5-turbo"
    http    *http.Client  // Shared client with 30s timeout
}

func newOpenAIClient(baseURL, apiKey, model string) autoapply.LLMClient {
    return &openAIClient{
        baseURL: baseURL,
        apiKey:  apiKey,
        model:   model,
        http:    &http.Client{Timeout: 30 * time.Second},
    }
}
```
Factory function creates an LLM client implementing the `autoapply.LLMClient` interface.

```go
func (c *openAIClient) Complete(ctx context.Context, system, user string) (string, error) {
    body, _ := json.Marshal(map[string]any{
        "model": c.model,
        "messages": []map[string]string{
            {"role": "system", "content": system},
            {"role": "user", "content": user},
        },
        "temperature": 0,  // Deterministic (no randomness)
    })
```
Builds the OpenAI API request body:
- **temperature: 0**: No randomness, always pick the most likely token (best for form filling)

```go
    req, err := http.NewRequestWithContext(ctx, http.MethodPost,
        c.baseURL+"/chat/completions", bytes.NewReader(body))
    if err != nil {
        return "", err
    }
    req.Header.Set("Content-Type", "application/json")
    if c.apiKey != "" {
        req.Header.Set("Authorization", "Bearer "+c.apiKey)
    }
```
- Creates HTTP POST request to `/chat/completions` endpoint
- Sets Content-Type header
- If API key provided, adds Authorization header

```go
    resp, err := c.http.Do(req)
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()

    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
        return "", fmt.Errorf("llm: http %d: %s", resp.StatusCode, string(body))
    }
```
- Sends the request
- If non-2xx status: read up to 8KB of response and return error

```go
    var out struct {
        Choices []struct {
            Message struct {
                Content string `json:"content"`
            } `json:"message"`
        } `json:"choices"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
        return "", fmt.Errorf("llm: decode response: %w", err)
    }
    if len(out.Choices) == 0 {
        return "", fmt.Errorf("llm: empty choices")
    }
    return out.Choices[0].Message.Content, nil
```
- Parses OpenAI response JSON structure
- Extracts the assistant's message from the first choice
- Returns the content (the LLM's reply)

---

## 2. apps/autoapply/service/handler.go (Core Event Handler)

See detailed explanations in the comprehensive document. Key points:

**Main flow**:
1. Unmarshal event
2. Check enabled, required fields, score threshold
3. Fast-path idempotency check
4. Atomic slot reservation (UNIQUE partial index prevents race)
5. Download CV with SSRF guards
6. Build SubmitRequest
7. Route through registry
8. Finalize: update status, mark match, emit events

**isUniqueViolation**: Detects PostgreSQL SQLSTATE 23505 (duplicate key constraint violation)

**urlHost**: Extracts hostname from URL for logging

---

## 3. apps/autoapply/service/cvdownload.go

**SSRF Guards** (prevent Server-Side Request Forgery attacks):

```go
func validateCVURL(rawURL string) error {
    u, err := url.Parse(rawURL)
    if err != nil {
        return err
    }
    if !strings.EqualFold(u.Scheme, "https") {
        return fmt.Errorf("scheme must be https, got %q", u.Scheme)
    }
```
- Only allows HTTPS (blocks http://, file://, ftp://, etc.)

```go
    host := u.Hostname()
    if host == "" {
        return errors.New("missing host")
    }
    addrs, err := net.LookupIP(host)
    if err != nil {
        return fmt.Errorf("dns: %w", err)
    }
    for _, ip := range addrs {
        if isPublicIP(ip) {
            return nil
        }
    }
    return fmt.Errorf("host %q resolves only to non-public addresses", host)
}
```
- Performs DNS lookup
- Rejects if ALL returned IPs are non-public (accepts if ANY IP is public)

```go
func isPublicIP(ip net.IP) bool {
    if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
        ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
        return false
    }
    if ip.Equal(net.IPv4(169, 254, 169, 254)) {
        return false  // AWS metadata address
    }
    return true
}
```
- Returns false for: loopback (127.x.x.x), private (10.x, 172.16-31.x, 192.168.x), link-local, multicast, AWS metadata (169.254.169.254)

```go
func deriveFilename(resp *http.Response, rawURL string) string {
    if cd := resp.Header.Get("Content-Disposition"); cd != "" {
        if _, params, err := mimeParse(cd); err == nil {
            if name := params["filename"]; name != "" {
                return sanitise(name)
            }
        }
    }
    if u, err := url.Parse(rawURL); err == nil {
        base := path.Base(u.Path)
        if base != "" && base != "/" && base != "." && strings.Contains(base, ".") {
            return sanitise(base)
        }
    }
    return "resume.pdf"
}
```
- Prefers Content-Disposition header filename
- Falls back to URL path basename
- Defaults to "resume.pdf" if all else fails

```go
func sanitise(name string) string {
    name = strings.ReplaceAll(name, "\\", "/")
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
- Strips both / and \ path separators (prevents path traversal)
- Only allows [A-Za-z0-9._-]
- Limits to 64 characters

---

## 4. pkg/autoapply/registry.go

```go
type Registry struct {
    tiers []Submitter
}

func NewRegistry(tiers ...Submitter) *Registry {
    return &Registry{tiers: tiers}
}

func (r *Registry) Submit(ctx context.Context, req SubmitRequest) (SubmitResult, error) {
    log := util.Log(ctx).
        WithField("candidate_id", req.CandidateID).
        WithField("apply_url", req.ApplyURL)

    for _, s := range r.tiers {
        if !s.CanHandle(req.SourceType, req.ApplyURL) {
            continue
        }
        log.WithField("submitter", s.Name()).Debug("autoapply: routing to submitter")
        res, err := s.Submit(ctx, req)
        if err != nil {
            return SubmitResult{}, err
        }
        return res, nil
    }

    log.Debug("autoapply: no submitter matched; skipping")
    return SubmitResult{Method: "skipped", SkipReason: "no_submitter"}, nil
}
```

**Chain of Responsibility Pattern**:
- Tries each submitter in order
- First `CanHandle()` that returns true → calls `Submit()`
- If submit returns error → propagate (transient failure)
- If submit returns result → return immediately (no more tiers)
- If no tier matches → return skip/no_submitter

---

## 5. pkg/autoapply/browser/client.go

```go
type Pool struct {
    timeout   time.Duration
    userAgent string
    slots     chan *instance       // Channel for slot management
    instances []*instance
    closed    bool
    closeMu   sync.Mutex
}

type instance struct {
    mu      sync.Mutex
    browser *rod.Browser
}

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
```

**Architecture**:
- **slots channel**: Semaphore for concurrency control (size = pool size)
- **instances array**: Holds all browser processes
- Each instance has its own mutex (per-instance locking)
- Lazy launch: browsers created on first `acquire()` call

```go
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
```

**Slot Management**:
- `acquire()`: Pulls slot from channel (blocks if full)
- `release()`: Returns slot to channel
- If closed: discard slot (don't re-queue)

```go
func (i *instance) ensure() error {
    if i.browser != nil {
        if _, err := i.browser.Version(); err == nil {
            return nil
        }
        _ = i.browser.Close()
        i.browser = nil
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
        return fmt.Errorf("browser: launch: %w", err)
    }

    b := rod.New().ControlURL(u)
    if err := b.Connect(); err != nil {
        return fmt.Errorf("browser: connect: %w", err)
    }
    i.browser = b
    return nil
}
```

**Health Check & Launch**:
- If browser exists: call `Version()` to check if alive
- If health check fails: close and relaunch
- **Launcher flags**:
  - `Headless(true)`: No display server needed
  - `Leakless(true)`: Clean up resources
  - `disable-gpu`: Not needed headless
  - `no-sandbox`: Docker-friendly
  - `disable-dev-shm-usage`: Docker-friendly (use regular /tmp)
  - `disable-blink-features=AutomationControlled`: Defeat automation detection

```go
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
        _ = inst.browser.Close()
        inst.browser = nil
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
        return ErrCAPTCHA
    }

    for sel, val := range opts.TextFields {
        if val == "" {
            continue
        }
        el, err := page.Timeout(5 * time.Second).Element(sel)
        if err != nil {
            continue  // Non-required field; skip silently
        }
        _ = el.SelectAllText()
        _ = el.Input(val)
    }
```

**Submission Flow**:
1. Acquire instance slot (blocks if full)
2. Lock instance (exclusive use)
3. Ensure browser is running
4. Create blank page
5. Set user agent (fingerprinting)
6. Navigate to apply URL
7. Wait for page stability (2s)
8. Check for CAPTCHA
9. Fill text fields by CSS selector

```go
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
        return ErrCAPTCHA
    }

    if !verifySubmit(page, opts) {
        return ErrSubmitNotConfirmed
    }

    return nil
}
```

**File Upload & Submit**:
1. Write CV bytes to temp file
2. Set file input to temp file path
3. Find submit button (10s timeout)
4. Click submit
5. Wait for page stability (3s)
6. Check CAPTCHA again
7. Verify submission (confirmation selector or text)

```go
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
```
Checks HTML for CAPTCHA iframe keywords (case-insensitive).

```go
func verifySubmit(page *rod.Page, opts SubmitOptions) bool {
    if opts.ConfirmSel == "" && opts.ConfirmText == "" {
        return true  // Legacy: trust the click
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
```
Verifies submission by checking for confirmation signals (CSS selector or text).

---

(Document continues with all ATS submitters, session replay, LLM form filler, and email fallback implementations. Please request specific sections if you want me to continue with those explanations.)
