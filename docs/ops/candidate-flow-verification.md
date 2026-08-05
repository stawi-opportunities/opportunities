# Candidate flow verification

Step-by-step verification of the Stawi Opportunities **candidate journey**:
onboarding → CV → subscription → matching → iterative updates.

**Inventory source:** session scratch `flow-inventory.md` (mirrors
`apps/matching/cmd/main.go` + `ui/app/src` islands).

**How to read this doc:** each section lists **user-visible outcome**, **inputs**,
**APIs/UI actions**, **expected result**, and **evidence** path. Status is
`PASS` | `FAIL` | `ENV-BLOCKED` with notes.

Gateway prefix: browser calls `https://…/matching/<path>`; handlers below are
relative to the matching service mux.

---

## 0. Prerequisites

| Item | Notes |
|------|--------|
| Auth | OIDC JWT (prod) or test `X-Candidate-ID` when OIDC unset |
| Product DB | Neon product DB (candidates, matches, opportunities) |
| Files | `FILE_SERVICE_URI` → platform-files; ReBAC/content_upload live |
| Chat agent | `CHAT_AGENT_ENABLED` + `CHAT_AGENT_SERVICE_URI` (required for `/me/chat`) |
| Billing | `BILLING_SERVICE_URI` / `CHECKOUT_SERVICE_URI` + webhook secret |
| Evidence root | Implementer scratch under goal session (see plan `{SCRATCH}`) |

---

## 1. Onboarding

### User-visible flow

1. User signs in (Nav / Signup / Pricing CTA).
2. If subscription is not active/past_due/trial and no content return path → redirect to **`/onboarding/`**.
3. Wizard (kind-specific flow: job / scholarship / tender / deal / funding) collects preferences; optional preference chat.
4. Final submit promotes draft to profile and triggers initial match event.

### Inputs (examples)

| Field | Example | Source |
|-------|---------|--------|
| roles / titles | “software engineer” | job-onboarding-v1 |
| location | “Nairobi” | wizard |
| kind | job | flow id |

### APIs

| Step | Method + path | Expected |
|------|---------------|----------|
| Load draft | `GET /me/onboarding` | 200 + draft JSON (or empty) |
| Save step | `PUT /me/onboarding` | 200; draft persisted |
| Chat (optional) | `POST /me/chat` | 200 agent turns; 502/503 if chat-agent down |
| Complete | `POST /candidates/onboard` | 200; profile promoted; free tier |

### Status

| Check | Result | Evidence |
|-------|--------|----------|
| GET/PUT onboarding | **PASS** | `{SCRATCH}/flows/onboarding.log` (handler tests) |
| POST candidates/onboard | **PASS** | same + Success/field validation tests |
| Post-login redirect unpaid → onboarding | **PASS** | `ui/app/src/auth/postLoginRedirect.test.ts` (8 tests) |

---

## 2. CV upload

### User-visible flow

1. From **Dashboard → CV** (or onboarding CV step), user selects a PDF/DOCX.
2. Upload succeeds; UI shows file ref / qualifications summary when ready.
3. Background: extract → improve → embed (may be deferred if inference unset).

### Inputs

| Input | Example |
|-------|---------|
| File | `sample-cv.pdf` (multipart or PUT body per handler) |
| Candidate | JWT subject / `X-Candidate-ID` in tests |

### APIs

| Step | Method + path | Expected |
|------|---------------|----------|
| Upload | `PUT /me/cv` (preferred) or `POST /candidates/cv/upload` | 200; file-id on profile |
| Read | `GET /me/cv` | 200; file metadata + qualifications |
| Optional score | `POST /me/tools/cv-score` | 200 scores |

### Status

| Check | Result | Evidence |
|-------|--------|----------|
| PUT me/cv archives + enqueue extract | **PASS** | `{SCRATCH}/flows/cv-upload.log` (`TestMeCVHandlerArchivesAndEnqueues`) |
| Reject missing file part | **PASS** | same |
| Legacy POST upload | **PASS** | `TestUploadHandlerArchivesAndEnqueues` |
| E2E upload→extract→improve→embed | **PASS** | `TestCandidatesE2EUploadToEmbedding` |
| ReBAC files path (prod) | **PASS** (deployed) | platform-files `v1.10.59`, Keto `content_upload` for members/services |

---

## 3. Subscription / billing

### User-visible flow

1. Onboarding **PlanSelector** or Dashboard **Settings → Subscription**.
2. User picks plan → checkout redirect/hosted page.
3. Return + poll → subscription becomes active → dashboard unlock.

### Inputs

| Input | Example |
|-------|---------|
| plan_id | from `GET /billing/plans` |
| return URL | site dashboard/onboarding |

### APIs

| Step | Method + path | Expected |
|------|---------------|----------|
| Catalog | `GET /billing/plans` | 200 plan list (public) |
| State | `GET /me/subscription` | 200 status none\|active\|… |
| Start | `POST /billing/checkout` | 200/302 checkout URL |
| Poll | `GET /billing/checkout/status` | 200; activation when paid |
| Lifecycle | cancel / change-plan / invoices / usage-history | coherent JSON |

### Status

| Check | Result | Evidence |
|-------|--------|----------|
| GET /billing/plans (handler + prod) | **PASS** | `{SCRATCH}/flows/subscription.log`; prod HTTP 200 catalog starter/managed |
| GET /me/subscription status mapping | **PASS** | free→none, paid→active, trial→active, cancelled retained |
| Checkout unknown plan / nil store / gateway down | **PASS** (fail-closed) | handler tests |
| Checkout end-to-end paid rails | **ENV-BLOCKED** | requires live OIDC candidate JWT + payment provider; webhook/HMAC covered offline |

---

## 4. Matching against jobs

### User-visible flow

1. Paid/active (or free with limited tools) user opens **Dashboard → Matches**.
2. Feed shows opportunities from match store + saved/applications.
3. User can open apply details, save, apply, dismiss, refresh.

### Inputs

| Input | Notes |
|-------|--------|
| Candidate profile + prefs + CV signals | From onboarding/CV |
| Opportunities in product DB | Seeded or crawled |

### APIs

| Step | Method + path | Expected |
|------|---------------|----------|
| Feed | `GET /me/opportunities` | 200 list (empty only if no jobs/matches) |
| Legacy match | `GET /candidates/match` | 200 |
| Refresh | `POST /me/matches/refresh` | 200 recompute |
| Apply details | `GET /me/opportunities/{id}/apply` | 200 |
| Save / apply | `POST /me/saved-jobs`, `POST /me/applications` | 200 |

### Status

| Check | Result | Evidence |
|-------|--------|----------|
| GET /me/opportunities filters | **PASS** | `{SCRATCH}/flows/match.log` |
| saved-jobs star/unstar | **PASS** | same |
| applications POST | **PASS** | same |
| /api/me/* Phase-4 handlers (integration + testcontainers) | **PASS** | `{SCRATCH}/flows/me-v1-integration.log` |
| match-kinds (prod) | **PASS** | HTTP 200 `job`, `scholarship` |
| Live match against prod DB for a real user | **ENV-BLOCKED** | needs candidate JWT; offline path covered by handlers + preference-match events |

---

## 5. Iterative updates

### User-visible flow

1. User changes preferences (panel/chat) and/or re-uploads CV.
2. User refreshes matches or waits for async preference match.
3. Feed / stored prefs reflect new inputs without new account.

### APIs

| Step | Method + path | Expected |
|------|---------------|----------|
| Pref update | PUT onboarding / chat / rules | 200 |
| CV re-upload | PUT `/me/cv` | 200 new file-id |
| Rematch | POST `/me/matches/refresh` | 200; state differs or recomputed |
| Re-list | GET `/me/opportunities` | 200 |

### Status

| Check | Result | Evidence |
|-------|--------|----------|
| Preferences emit match event | **PASS** | `{SCRATCH}/flows/iterative-update.log` |
| CV re-upload enqueues pipeline | **PASS** | MeCV handler tests |
| PreferenceMatchHandler per enabled kind | **PASS** | events/v1 tests |
| Rematch refresh (me/v1 package) | **PASS** | package tests pass (refresh/dismiss/notifications covered in handlers_test) |

---

## 6. UI shell (optional browser)

| Check | Result | Evidence |
|-------|--------|----------|
| UI vitest suite | **PASS** (79/79) | `{SCRATCH}/ui/npm-test.log` |
| OpportunityCard / OpportunitiesFeed | **PASS** (fixed AuthProvider mocks) | `{SCRATCH}/ui/card-feed-fix.log` |
| Onboarding / AuthCallback / PreferenceChat | **PASS** | npm-test.log |
| Browser screenshots | **ENV-BLOCKED** | no authenticated browser session in this run; vitest + public HTTP smoke used |

---

## Package tests

| Package | Result | Evidence |
|---------|--------|----------|
| matching / placement / billing / httpmw | **PASS** (2026-08-05) | `{SCRATCH}/go-test-matching.log` |

---

## Residual risks / env blockers

- Production OIDC, payment checkout, and chat-agent may be unavailable in sandbox; offline criteria use testcontainers + `X-Candidate-ID` patterns already in repo tests.
- Trustage migrate SQL bug and checkout permission 403 (platform) are outside matching binary but noted in release notes.

## Changelog of verification runs

| Date | What ran | Outcome |
|------|----------|---------|
| 2026-08-05 | Inventory + skeleton | Created flow-inventory + this doc |
| 2026-08-05 | go test httpmw/billing/placement/matching/apps/matching | All packages ok |
| 2026-08-05 | Onboarding+CV+sub+match+iterative handler tests + UI suite + prod plans/match-kinds | PASS offline; checkout E2E ENV-BLOCKED |
| 2026-08-05 | Fixed OpportunityCard/Feed tests (useAuth without provider) | UI 79/79 |
| 2026-08-05 | Fixed me/v1 Mount default auth + integration tests (401→200) | PASS with testcontainers |
