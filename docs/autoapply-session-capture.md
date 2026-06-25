# Auto-Apply Session Capture — Design

**Status:** Draft · **Owner:** auto-apply · **Branch:** `feature/auto-apply`

## 1. Context and goals

The current auto-apply pipeline ([apps/autoapply](../apps/autoapply)) submits applications through a tiered `Submitter` registry: ATS-specific UI handlers → LLM-guided form fill → email fallback. This works for ATSes that expose unauthenticated apply pages (Greenhouse, Lever, public Workday tenants), but fails on regional and account-gated boards such as **BrighterMonday**, LinkedIn, Indeed, Jobberman, and most Workday tenants behind SSO. Reasons: captchas, MFA, device fingerprinting, ToS prohibitions on automated login.

**Goal.** Allow a candidate to log into a source themselves (in their own browser, with their own device fingerprint, satisfying captcha and MFA), capture the resulting session, store it server-side, and reuse it to submit applications on their behalf. Build this as a **per-source pluggable mechanism** so the same scaffolding serves every authenticated source.

**Non-goals.**
- Replacing the existing ATS submitters where they already work.
- Automating the *login* itself. The user always performs the login interactively.
- Headless-browser replay. Phase 1 is HTTP-cookie replay only; headless is a later concern (see §11).

## 2. Architecture

```
┌─────────────────┐        ┌─────────────┐         ┌────────────────────────┐
│ Web UI          │  JWT   │  Gateway    │  JWT    │ apps/matching          │
│ @stawi/auth-    │ ─────▶ │ (verifies   │ ──────▶ │  /pairings             │
│ runtime, OIDC   │        │  signature) │         │   profileIDFromJWT     │
└─────────────────┘        └─────────────┘         │   GetByProfileID       │
        │                                          │   mint pair code (V)   │
        │ shows 6-digit code                       └────────────────────────┘
        ▼                                                       │
┌─────────────────┐        ┌────────────────────────┐           │
│ User's Browser  │ redeem │ apps/matching          │ ◀─────────┘
│  + Extension    │ ─────▶ │  /pairings/redeem      │
│                 │        │   (unauth; code = auth)│
│  1. User logs   │        │   GetDel pair code     │
│     in to       │        │   mint refresh/access  │
│     brighter-   │        │   tokens (V)           │
│     monday      │        └────────────────────────┘
│                 │ capture            │
│  2. Extension   │   POST             │  Stawi access token (Bearer)
│     captures    │ ─────────────────▶ ▼
│     cookies     │        ┌────────────────────────┐
└─────────────────┘        │ apps/matching          │
                           │  /candidates/me/       │
                           │   sessions/:source     │
                           │   verify token (V)     │
                           │   envelope-encrypt     │
                           │   upsert Postgres      │
                           │   emit                 │
                           │   SessionCapturedV1    │
                           └────────────────────────┘
                                       │
                                       │ candidate_sessions (encrypted)
                                       ▼
   AutoApplyIntentV1 ──▶ apps/autoapply (handler.go)
                         ├─ exists check
                         ├─ reserve pending row
                         ├─ SessionLookup ─────┐
                         └─ router.Submit() ───┤
                                               ▼
                         pkg/autoapply/sessionsubmitter
                           load cookies → http.Client + jar
                           GET apply form → POST multipart
                           detect 302→login → revoke + SessionExpiredV1
```

`(V)` = Valkey/Redis. `JWT` = OIDC JWT from `@stawi/auth-runtime`; the gateway verifies the signature and the downstream Go service reads only the `sub` claim (see `profileIDFromJWT` at [apps/api/cmd/main.go:273-296](../apps/api/cmd/main.go#L273)). `Stawi access token` = opaque HMAC-signed token minted by `/pairings/redeem`, verified locally without an IdP roundtrip — keeps the extension off the OIDC critical path.

Two timelines run independently:
- **Capture timeline** is user-driven and asynchronous (user logs in whenever they like; extension uploads on cookie change).
- **Apply timeline** is queue-driven; if a session is missing or expired when an intent arrives, the handler emits `SessionRequiredV1` and the UI surfaces a "Re-connect your account" CTA.

## 3. Source auth manifest

A new manifest in `definitions/source-auth/<source_type>.yaml` declares per-source auth metadata. Manifests are loaded at startup the same way other registries are (see how [`definitions/opportunity-kinds`](../definitions) is consumed by [pkg/repository](../pkg/repository)).

```yaml
# definitions/source-auth/brightermonday.yaml
source_type: brightermonday          # matches domain.SourceBrighterMonday
auth_method: extension               # none | extension | oauth | api_token
display_name: BrighterMonday Kenya
login_url: https://www.brightermonday.co.ke/login
home_url: https://www.brightermonday.co.ke
cookie_domains:
  - .brightermonday.co.ke
storage_keys: []                     # optional localStorage keys to capture
required_cookies:                    # heuristic for "session looks valid"
  - laravel_session
  - XSRF-TOKEN
session_ttl: 720h                    # 30 days; replay also detects expiry live
detect_logged_in:                    # optional sanity probe
  method: GET
  url: https://www.brightermonday.co.ke/api/v1/me
  expect_status: 200
apply_flow:
  type: http_form
  form_url_pattern: '^https://www\.brightermonday\.co\.ke/job/.+/apply$'
  fields:
    cv_file:    { name: "resume", source: "cv_bytes" }
    cover:      { name: "cover_letter", source: "cover_letter" }
    phone:      { name: "phone", source: "phone" }
  csrf:
    source: cookie       # cookie | form_token
    cookie: XSRF-TOKEN
    header: X-XSRF-TOKEN
instructions_md: |
  ### Connect BrighterMonday
  1. Install the Stawi extension from the Chrome / Firefox store.
  2. Open <https://www.brightermonday.co.ke/login> and sign in.
  3. Click the Stawi extension icon and choose **"Connect this account"**.
  4. Return to Stawi — the BrighterMonday card should show **Connected ✓**.
```

The manifest is also served by `GET /api/v1/sources/auth-manifest`, which the extension fetches at install time so it knows which domains to watch.

## 4. Data model

### 4.1 New table — `db/migrations/0009_candidate_sessions.sql`

Follows the conventions established in [db/migrations/0008_candidate_applications.sql](../db/migrations/0008_candidate_applications.sql) (VARCHAR(20) PK, `*_at TIMESTfinAMPTZ`, partial unique index, soft delete).

```sql
CREATE TABLE IF NOT EXISTS candidate_sessions (
    id              VARCHAR(20)  PRIMARY KEY,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ,

    candidate_id    VARCHAR(20)  NOT NULL,
    source_type     VARCHAR(40)  NOT NULL,    -- matches domain.SourceType
    -- Envelope-encrypted payload: AES-256-GCM(plaintext, DEK), with DEK wrapped by master.
    payload_enc     BYTEA        NOT NULL,
    payload_nonce   BYTEA        NOT NULL,
    dek_wrapped     BYTEA        NOT NULL,
    key_id          VARCHAR(64)  NOT NULL,    -- master key identifier (for rotation)

    captured_at     TIMESTAMPTZ  NOT NULL,
    expires_at      TIMESTAMPTZ,              -- inferred from cookies or manifest ttl
    last_used_at    TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ,
    user_agent      VARCHAR(255) NOT NULL DEFAULT '',
    capture_origin  VARCHAR(40)  NOT NULL DEFAULT 'extension'  -- extension | remote_browser
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_candidate_sessions_active
    ON candidate_sessions (candidate_id, source_type)
    WHERE deleted_at IS NULL AND revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_candidate_sessions_expiry
    ON candidate_sessions (expires_at)
    WHERE deleted_at IS NULL AND revoked_at IS NULL;
```

### 4.2 Domain types — `pkg/domain/session.go` (new)

```go
type CandidateSession struct {
    ID             string
    CandidateID    string
    SourceType     SourceType
    PayloadEnc     []byte
    PayloadNonce   []byte
    DEKWrapped     []byte
    KeyID          string
    CapturedAt     time.Time
    ExpiresAt      *time.Time
    LastUsedAt     *time.Time
    RevokedAt      *time.Time
    UserAgent      string
    CaptureOrigin  string
    CreatedAt      time.Time
    UpdatedAt      time.Time
    DeletedAt      gorm.DeletedAt
}

type SessionPayload struct {        // plaintext shape; never persisted
    Cookies []SessionCookie `json:"cookies"`
    Headers map[string]string `json:"headers"`
    Storage map[string]string `json:"storage"` // localStorage snapshot
}

type SessionCookie struct {
    Name, Value, Domain, Path string
    Expires                   *time.Time
    HttpOnly, Secure          bool
    SameSite                  string
}
```

### 4.3 Pairing codes and tokens — Valkey only

Short-lived state lives in Valkey ([pkg/kv](../pkg/kv)) with TTL — no Postgres durability needed:

| Key | Value | TTL | Purpose |
|---|---|---|---|
| `pair:<code>` | `candidate_id` | 5 min | One-time pair code; consumed with `GETDEL` |
| `tok:<random_prefix>` | `{candidate_id, kind, issued_at, expires_at}` JSON | 15 min (access) / 30 d (refresh) | Stawi access + refresh tokens, see §6.4 |
| `revoked:<candidate_id>` | issued-before timestamp | 30 d | Tombstone written when a user clicks "Disconnect extension" — any token issued before this is rejected |
| `pair_attempts:<ip>` | counter | 1 min | Rate limiter (5 attempts per IP per minute) |

## 5. Cryptography

**Envelope encryption.** A per-session 32-byte data encryption key (DEK) encrypts the payload with AES-256-GCM; the DEK itself is wrapped (also AES-256-GCM) with a master key loaded from the `SESSION_MASTER_KEY` env var (base64-encoded 32 bytes). Wrapping the DEK rather than encrypting the payload directly with the master key lets us rotate masters without re-encrypting all rows: a row's `key_id` tells us which master to use to unwrap its DEK.

**Why not just AES-256-GCM with the master?** Same security for one row, but rotation requires re-encrypting every row. Envelope wrapping keeps rotation O(rows-changed) instead of O(all rows).

**Key handling.** `SESSION_MASTER_KEY` is loaded at process start; never logged; never returned by any API. If the env var is missing, `apps/autoapply` and the session-write endpoints refuse to start (fail-closed).

**Future swap to KMS.** The crypto package exposes `Wrap(dek []byte) ([]byte, keyID string, error)` and `Unwrap(wrapped []byte, keyID string) ([]byte, error)`. Adding a `KMSWrapper` later is a drop-in.

## 6. Extension ↔ API protocol

### 6.1 Pairing (one-time, install)

The web UI is already an OIDC client via [@stawi/auth-runtime](../ui/app/src/providers/AuthProvider.tsx). All it has to do is call `runtime.fetch("/pairings", {method:"POST"})` — the runtime attaches the Bearer header, the gateway verifies the signature, and the downstream handler trusts the `sub` claim (same pattern the codebase already uses, e.g. `profileIDFromJWT` at [apps/api/cmd/main.go:273-296](../apps/api/cmd/main.go#L273) and the candidate-attributed admin actions at [apps/api/cmd/sources_admin.go:483](../apps/api/cmd/sources_admin.go#L483)).

```
User                  Web UI                       Gateway                  apps/matching                Extension
  │                     │                           │                          │                            │
  │ "Pair extension"────▶                           │                          │                            │
  │                     │  POST /pairings           │   POST /pairings         │                            │
  │                     │  Authorization: Bearer    │   Authorization: Bearer  │                            │
  │                     │  <oidc-jwt>               │   <oidc-jwt>             │                            │
  │                     ├──────────────────────────▶├─────────────────────────▶│                            │
  │                     │                           │                          │ profile_id = sub(jwt)      │
  │                     │                           │                          │ candidate_id = repo.       │
  │                     │                           │                          │   GetByProfileID(...)      │
  │                     │                           │                          │ code = randHex(6)          │
  │                     │                           │                          │ valkey.SetEX("pair:"+code, │
  │                     │                           │                          │   candidate_id, 5m)        │
  │                     │  { code, expires_in: 300 }                           │                            │
  │                     │◀────────────────────────────────────────────────────┤                            │
  │ ◀ display 6-digit ──┤                                                                                  │
  │   code              │                                                                                  │
  │                     │                                                                                  │
  │ types code in ext ──────────────────────────────────────────────────────────────────────────────────▶│
  │                                                                          │   POST /pairings/redeem     │
  │                                                                          │   (unauthenticated;         │
  │                                                                          │    the code IS the auth)    │
  │                                                                          │◀──────────────────────────┤
  │                                                                          │ candidate_id, ok =        │
  │                                                                          │   valkey.GetDel("pair:"+   │
  │                                                                          │     code)                  │
  │                                                                          │ refresh, access =          │
  │                                                                          │   mintTokens(candidate_id) │
  │                                                                          │   { refresh, access,       │
  │                                                                          │     candidate_id,          │
  │                                                                          │     access_expires_in:900, │
  │                                                                          │     refresh_expires_in:    │
  │                                                                          │       2592000 }            │
  │                                                                          ├──────────────────────────▶│
```

- **Pair code**: 6 alphanumeric chars (base32 over 30 bits ≈ 1×10⁹ space), single-use, ≤ 5 min TTL, IP-rate-limited (5 redemption attempts per IP per minute).
- **Refresh token**: opaque, candidate-scoped, 30-day TTL, revocable. Extension exchanges via `POST /pairings/refresh`.
- **Access token**: opaque, 15-min TTL, presented as `Authorization: Bearer …` on every capture upload.
- The extension stores tokens in `chrome.storage.local` (encrypted by the browser profile); never `localStorage`.

### 6.4 Local token verification

Stawi-issued tokens are **not** OIDC JWTs — they are opaque server-side tokens whose only job is to authenticate the extension to our API. Two reasons to keep them off the OIDC critical path:
1. Extensions are hostile to OIDC redirect flows (no fixed redirect URI, popups are blocked, MV3 service workers can't hold a long-lived session).
2. We want a one-button revoke ("Disconnect extension") that doesn't affect the user's main web session.

**Token shape.** `<base64url(random 16 bytes)>.<base64url(HMAC-SHA256(payload, SESSION_TOKEN_KEY))>`. The random prefix is the lookup key; the HMAC binds it. On verify: split, recompute HMAC in constant time, then `GET` the prefix from Valkey. The Valkey value carries `{candidate_id, kind: access|refresh, issued_at, expires_at}`.

**Storage.** Valkey, keyed `tok:<prefix>`. TTL set on insert so eviction is automatic. Revoke = `DEL tok:<prefix>` (plus a `revoked:<candidate_id>:<issued_before>` tombstone so the matching refresh token can't mint new access tokens).

**Why not JWTs?** JWTs would require key distribution between issuer and verifier (we'd be both, so it works), but they're not revocable without a denylist anyway — and once you have a denylist you've added a Valkey roundtrip on every request, which is what opaque-token-lookup already costs. Opaque is simpler and the security posture is identical.

**Key.** `SESSION_TOKEN_KEY` (base64 32 bytes) loaded at process start. Distinct from `SESSION_MASTER_KEY` (the envelope-encryption master) so a compromise of one doesn't compromise the other.

### 6.2 Capture

When the extension's cookie-change listener fires on a domain it recognizes (from the auth manifest), it gathers the relevant cookies, performs the optional `detect_logged_in` probe to check the session is *actually* logged in (avoids uploading a logged-out anonymous session), then:

```
POST /api/v1/candidates/me/sessions/:source_type
Authorization: Bearer <access_token>
Content-Type: application/json

{
  "captured_at": "2026-05-18T10:23:00Z",
  "user_agent": "Mozilla/5.0 ...",
  "cookies": [{ "name": "...", "value": "...", "domain": "...", ... }],
  "headers": { "Accept-Language": "en-US,en;q=0.9" },
  "storage": { "...": "..." }
}
```

Server validates the source_type is known and auth_method=`extension`, encrypts, upserts (replacing any prior row), emits `SessionCapturedV1`.

### 6.3 Replay handshake

Sessionsubmitter loads the session, reconstructs an `http.Client` with a `cookiejar.Jar` pre-seeded with the cookies, sets `User-Agent` and `Accept-Language` from the captured headers (matching the user's real browser reduces fingerprint mismatch), and proceeds with form scrape + POST.

Expiry detection: if the apply GET returns a 3xx to a login URL, a 401, or HTML containing a manifest-declared `logged_out_marker`, mark session revoked + emit `SessionExpiredV1`.

## 7. Replay path

```go
// pkg/autoapply/sessionsubmitter/submitter.go
type Submitter struct {
    manifests authmanifest.Store
    sessions  authsession.SessionProvider
}

func (s *Submitter) CanHandle(srcType domain.SourceType, applyURL string) bool {
    m, ok := s.manifests.Get(srcType)
    return ok && m.AuthMethod == authmanifest.Extension && m.MatchesApplyURL(applyURL)
}

func (s *Submitter) Submit(ctx context.Context, req autoapply.SubmitRequest) (autoapply.SubmitResult, error) {
    sess, err := s.sessions.Session(ctx, req.CandidateID, string(req.SourceType))
    if errors.Is(err, authsession.ErrSessionRequired) {
        return autoapply.SubmitResult{Method: "skipped", SkipReason: "session_required"}, nil
    }
    if err != nil { return autoapply.SubmitResult{}, err } // transient

    client := newClientFromSession(sess)                    // injects cookies + UA + Accept-Language
    form, err := scrapeApplyForm(ctx, client, req.ApplyURL) // GET, parse with goquery
    if err != nil { return ..., err }                       // transient or terminal per manifest
    res, err := postMultipart(ctx, client, form, req)       // POST with CV bytes
    ...
}
```

Register in [apps/autoapply/cmd/main.go](../apps/autoapply/cmd/main.go) **before** the `browser` and `email_fallback` submitters so authenticated paths win for any source that has a manifest.

## 8. API surface

All endpoints live in **[apps/matching](../apps/matching/cmd/main.go#L213-L237)** — the matching service already owns the `/candidates/*` surface (CV upload, preferences, auto-apply toggle) and already publishes onto the autoapply queue (`SubjectAutoApplySubmit` at [cmd/main.go:191](../apps/matching/cmd/main.go#L191)), so notification events stay cohesive. Files:

```
apps/matching/service/http/v1/
  sessions.go     # /candidates/me/sessions handlers
  pairings.go     # /pairings handlers
  auth_manifest.go# /sources/auth-manifest handlers
```

Two auth styles, both already used in this codebase:

- **`gateway-jwt`** — request carries an OIDC JWT from `@stawi/auth-runtime`; the gateway verifies the signature; the handler reads `sub` via a shared `profileIDFromJWT` helper (promote out of [apps/api/cmd/main.go:273](../apps/api/cmd/main.go#L273) into a new `pkg/authz/`) and resolves `candidate_id` via [`CandidateRepository.GetByProfileID`](../pkg/repository/candidate.go#L43). Same pattern as the existing flag-admin and source-admin candidate-attribution.
- **`stawi-token`** — Stawi-issued opaque Bearer token (see §6.4), verified locally against Valkey. No gateway verification needed.

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `POST` | `/pairings` | `gateway-jwt` | Create one-time pair code; web UI calls via `runtime.fetch` |
| `POST` | `/pairings/redeem` | unauthenticated (code = auth) | Extension exchanges code for refresh + access token |
| `POST` | `/pairings/refresh` | refresh token | Mint a fresh access token |
| `POST` | `/pairings/revoke` | `gateway-jwt` | "Disconnect extension" — drops all tokens for this candidate |
| `GET` | `/sources/auth-manifest` | `stawi-token` | Manifest list (domains + capture rules; no instructions_md) |
| `GET` | `/sources/:source_type/auth` | `gateway-jwt` | Manifest + `instructions_md` for UI rendering |
| `POST` | `/candidates/me/sessions/:source_type` | `stawi-token` | Upload capture |
| `GET` | `/candidates/me/sessions` | `gateway-jwt` | List per-source status (no payload) |
| `DELETE` | `/candidates/me/sessions/:source_type` | `gateway-jwt` | Revoke one session |

**Rate limits.**
- `POST /pairings/redeem`: 5 attempts per IP per minute (Valkey counter).
- `POST /candidates/me/sessions/:source_type`: 1 upload per `(candidate_id, source_type)` per 30 s.
- `POST /pairings/refresh`: 10 per `refresh_token` per minute.

**Helper to promote.** `profileIDFromJWT` is currently duplicated-ready in `apps/api`; the matching service's existing handlers don't use it because they trust path params. Phase 3 lifts it into `pkg/authz/jwt.go` and both apps consume it. This is the smallest possible change to give matching real candidate-identity reads.

## 9. Threat model

| Threat | Mitigation |
|---|---|
| Database leak exposes cookies | Envelope encryption; master key never in DB; periodic rotation re-wraps DEKs without touching payloads. |
| Compromised API process reads plaintext sessions | Acceptable: this is the trust boundary. Limit blast radius by holding plaintext only during a single replay (no in-memory caching). |
| Malicious extension impersonator uploads someone else's session | Extension access token is candidate-scoped and short-lived; uploads are bound to the authenticated candidate; pairing requires user action in the authenticated web UI. |
| Pairing code brute force | 6 alphanumeric ≈ 36⁶ ≈ 2³¹; combined with ≤ 5 attempts and ≤ 5 min TTL the search space is closed. Rate limit by IP. |
| Session theft via XSS in main web app | Sessions never returned via any API. Tokens stored in HttpOnly cookies for the web UI. |
| Source ToS violation / detection | User performs the login (their consent); replay uses their UA and language headers; per-source rate limits in the executor; expose an "Off" switch per source. |
| Replay against a different IP triggers source-side fraud detection | Out of scope for v1 — acceptable risk. Mitigation path: proxy egress via a residential IP per candidate (expensive). |
| Stale sessions cause silent failures | `SessionExpiredV1` event + UI re-onboarding CTA; manifest-declared `detect_logged_in` probe re-validates before risky applies. |

## 10. Failure modes and observability

| Mode | Detection | UX |
|---|---|---|
| No session captured | `ErrSessionRequired` at lookup | `SessionRequiredV1` → UI card "Not connected" |
| Session present but expired (heuristic) | `expires_at < now` | UI card "Re-login needed", auto-skip apply |
| Session present but server says logged out | Replay 302→login or 401 | Auto-revoke + `SessionExpiredV1`, UI card flips to "Re-login needed" |
| Replay POST returns captcha HTML | Manifest `captcha_marker` substring match | Skip with `SkipReason="captcha"`, optionally emit notification |
| Source changed its form fields | Field map miss at scrape time | Skip with `SkipReason="form_changed"`, alert via existing autoapply telemetry |

New metrics (in `pkg/telemetry/`):
- `autoapply_session_lookup_total{source,outcome}` — `ok|missing|expired`
- `autoapply_session_replay_total{source,outcome}` — `submitted|expired|captcha|form_changed|error`
- `autoapply_session_age_seconds{source}` histogram on use

## 11. Rollout phases

| # | Slice | Verifiable when |
|---|---|---|
| 1 | `pkg/authsession` (interface, store, crypto) + migration 0009 + GORM repository | unit tests green; round-trip encrypt/decrypt; revoke + soft-delete work |
| 2 | Auth manifest loader (`pkg/authmanifest`) + BrighterMonday YAML + `GET /sources/auth-manifest`, `GET /sources/:t/auth` | `curl` returns manifest |
| 3 | Promote `profileIDFromJWT` → `pkg/authz/jwt.go`; add `pkg/authz/stawitok` (mint+verify); pairing + session-upload endpoints in `apps/matching/service/http/v1` | integration test: hit `/pairings` with a fake JWT (matching the `flags_admin_test.go` `fakeJWT` pattern) → redeem code → upload capture with the returned access token → `GET /candidates/me/sessions` shows the entry |
| 4 | Browser extension MVP (BrighterMonday only) — manifest, popup, content script, capture loop | install in local Chrome, log in to BrighterMonday, see green check in main UI |
| 5 | `sessionsubmitter` + BrighterMonday `apply_flow` execution + handler wiring (`SessionLookup` in `HandlerDeps`) | integration test: seed session → publish intent → assert `submitted` row |
| 6 | UI "Connected Accounts" page | manual flow: install ext → log in → see Connected → trigger intent → see Applied |
| 7 | Generalise: second source (Workday tenant or Jobberman) | same flow, new YAML only, no Go changes |
| 8 | Headless replay leg (`pkg/autoapply/sessionsubmitter/headless`) for sources where HTTP replay fails | LinkedIn / Workday-tenant integration test |

Phase 1–5 are the minimum for the "BrighterMonday end-to-end" milestone. Phases 6–8 are follow-ups.

## 12. Decisions and remaining unknowns

### Closed

1. ~~**Pairing token storage.**~~ **Valkey** (see §4.3 and §6.4). Codebase already depends on it; TTL-native; audit needs are covered by `SessionCapturedV1` / `SessionExpiredV1` flowing through the writer's Iceberg sink.
2. ~~**Form scrape engine.**~~ **`goquery`** for v1. BrighterMonday is server-rendered HTML; the apply form is a plain `<form>` POST. `chromedp`/`rod` enters the picture only when a source declares `apply_flow.type: headless` in its manifest, which gates Phase 8.
3. ~~**Multi-browser / multi-device.**~~ **Last-write-wins** for v1. The partial unique index in §4.1 already enforces one row per `(candidate_id, source_type)`; capture upserts. Reasoning: if a user captures from Chrome then Firefox, the freshest cookies are almost always the right ones — older sessions usually share the same backend identity and the older one will expire on its own. Multi-device-per-source revisits if a real user need surfaces.
4. ~~**Extension distribution.**~~ **Three-step rollout.**
   - **Phase 4 internal testing:** unpacked extension loaded via Chrome's "Developer mode" + Firefox's `web-ext run`. No signing, no store.
   - **Pre-GA:** submit to Chrome Web Store + Firefox AMO in parallel with Phase 5/6. Allow 1-2 weeks for Chrome review.
   - **Post-launch:** UI's "Install extension" CTA links to the store listings. We do **not** self-host signed CRXs for production Chrome users — modern Chrome silently disables side-loaded extensions outside enterprise policy, so self-hosting only works for AMO-signed Firefox XPIs as a fallback.
5. ~~**What is "candidate web session"?"**~~ **Closed.** Web UI uses `@stawi/auth-runtime` (OIDC); an upstream gateway verifies JWT signatures; downstream Go services trust the Bearer token. `apps/api` reads the `sub` claim unverified via `profileIDFromJWT` ([apps/api/cmd/main.go:273](../apps/api/cmd/main.go#L273)); `apps/matching`'s candidate-facing handlers don't read auth at all today. The session endpoints adopt the same gateway-trust pattern: decode `sub`, look up `candidate_id` via [`CandidateRepository.GetByProfileID`](../pkg/repository/candidate.go#L43). Frame's `SecurityManager` is wired only in `apps/crawler` admin paths today ([apps/crawler/cmd/main.go:293](../apps/crawler/cmd/main.go#L293)); we do not wire it into `apps/matching` for v1.

### Draft for legal review (blocks GA, not engineering)

6. **Candidate consent / disclosure copy.** Drafts below; legal must sign off before the extension lists publicly. The candidate is delegating credential-equivalent material; the language has to be explicit, action-bound, and revocable.

   *Extension first-run modal:*
   > **Connect your job-board accounts to Stawi**
   >
   > Stawi will store the session cookies for sources you connect (BrighterMonday, etc.) so it can submit applications on your behalf. We:
   > - Only access sources you explicitly connect.
   > - Encrypt your session cookies at rest and never expose them through any API.
   > - Let you disconnect any source — or revoke the whole extension — in one click from your Stawi dashboard.
   >
   > You remain responsible for each source's Terms of Service. Stawi acts under your authenticated session; we do not bypass any login control.
   >
   > **[I understand and consent]   [Cancel]**

   *Per-source "Connect" card (web UI):*
   > Connecting **BrighterMonday** lets Stawi submit applications to BrighterMonday job posts under your account. Stawi will not change your profile, send messages, or read your inbox. You can disconnect at any time.

### Still open (non-blocking for Phase 1)

7. **Gateway routing.** New `/pairings/*` and `/candidates/me/sessions/*` paths land on `apps/matching`, which is reachable via the same gateway as the existing `/candidates/cv/upload` etc. (presumed). Confirm with infra that no new route rule is needed — and if a new host or path prefix is required, decide that before Phase 3 ships. Does not block Phase 1 (the pure-Go session/crypto/repository code does not depend on routing).
