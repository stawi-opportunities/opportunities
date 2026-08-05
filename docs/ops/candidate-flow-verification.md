# Candidate flow verification

Step-by-step verification of the Stawi Opportunities **candidate journey**:
onboarding → CV → subscription → matching → iterative updates.

**Inventory source:** `{SCRATCH}/flow-inventory.md` (mirrors
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
| Auth | OIDC JWT (prod) or test `X-Candidate-ID` via `NewCandidateAuth(nil)` |
| Product DB | Neon product DB (prod) or testcontainers Postgres (integration) |
| Files | `FILE_SERVICE_URI` → platform-files; ReBAC `content_upload` live in prod |
| Chat agent | Production path is **MeChatAgentHandler** only (no local fallback) |
| Billing | Checkout needs live payment; lifecycle handlers tested fail-closed offline |
| Evidence root | `/tmp/grok-goal-1de09da3b359/implementer` (`{SCRATCH}`) |

---

## 1. Onboarding

### User-visible flow

1. User signs in (Nav / Signup / Pricing CTA).
2. If subscription is not active/past_due/trial and no content return path → redirect to **`/onboarding/`**.
3. Wizard (kind-specific flow) collects preferences; optional preference chat.
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
| Load draft | `GET /me/onboarding` | 200 + draft JSON (or empty → step 1) |
| Save step | `PUT /me/onboarding` | 204; draft persisted with `updated_at` |
| Chat (optional) | `POST /me/chat` | agent turns **or** 503 if chat-agent unset |
| Complete | `POST /candidates/onboard` | 200; profile promoted; free tier |

### Status

| Check | Result | Evidence |
|-------|--------|----------|
| GET/PUT onboarding | **PASS** | `{SCRATCH}/flows/onboarding.log` |
| POST candidates/onboard | **PASS** | same |
| Post-login redirect unpaid → onboarding | **PASS** | `ui/app/src/auth/postLoginRedirect.test.ts` (8 tests) |
| POST /me/chat (production agent path) | **PASS** (fail-closed) | `{SCRATCH}/flows/inventory-coverage.log` — nil client → 503 `chat_agent_unavailable` |
| POST /me/chat live agent conversation | **ENV-BLOCKED** | needs `CHAT_AGENT_SERVICE_URI` + OIDC; residual: SPA PreferenceChat tests cover UX shell |

---

## 2. CV upload

### User-visible flow

1. From **Dashboard → CV** (or onboarding), user selects a PDF/DOCX.
2. Upload succeeds; UI shows file ref / qualifications when ready.
3. Background: extract → improve → embed (may defer if inference unset).

### Inputs

| Input | Example |
|-------|---------|
| File | multipart `file` field (e.g. PDF) |
| Candidate | JWT subject / `X-Candidate-ID` in tests |

### APIs

| Step | Method + path | Expected |
|------|---------------|----------|
| Upload | `PUT /me/cv` or `POST /candidates/cv/upload` | 202; enqueue extract |
| Read | `GET /me/cv` | 200; `present` + file_id/qualifications |
| Optional score | `POST /me/tools/cv-score` | 200 scores **or** 503 if scorer unset |
| Optional fit | `POST /me/tools/job-fit` | 200 keyword/vector blend |

### Status

| Check | Result | Evidence |
|-------|--------|----------|
| PUT me/cv archives + enqueue | **PASS** | `{SCRATCH}/flows/cv-upload.log` |
| GET me/cv present/empty | **PASS** | `{SCRATCH}/flows/inventory-coverage.log` |
| E2E upload→extract→improve→embed | **PASS** | `TestCandidatesE2EUploadToEmbedding` in cv-upload.log |
| tools/job-fit keywords path | **PASS** | `TestJobFitHandler_*` in go-test / http/v1 |
| tools/cv-score without scorer | **PASS** (fail-closed 503) | inventory-coverage.log |
| tools/cv-score with live scorer | **ENV-BLOCKED** | needs Scorer + optional DB CV text |
| ReBAC files path (prod deploy) | **PASS** (deployed) | platform-files v1.10.59 + Keto content_upload |

---

## 3. Subscription / billing

### User-visible flow

1. Onboarding **PlanSelector** or Dashboard **Settings → Subscription**.
2. User picks plan → checkout redirect/hosted page.
3. Return + poll → subscription becomes active → dashboard unlock.

### APIs

| Step | Method + path | Expected |
|------|---------------|----------|
| Catalog | `GET /billing/plans` | 200 plan list (public) |
| State | `GET /me/subscription` | 200 status none\|active\|… |
| Start | `POST /billing/checkout` | 200/302 checkout URL |
| Poll | `GET /billing/checkout/status` | 200; activation when paid |
| Cancel | `POST /billing/cancel` | 200 schedule **or** 503 if store nil |
| Change plan | `POST /billing/change-plan` | 200/4xx **or** 503 if store nil |
| Invoices | `GET /billing/invoices` | 200 list (empty ok) |
| Usage | `GET /billing/usage-history` | 200 series (empty ok) |

### Status

| Check | Result | Evidence |
|-------|--------|----------|
| GET /billing/plans (handler + prod) | **PASS** | `{SCRATCH}/flows/subscription.log`; prod HTTP 200 starter/managed |
| GET /me/subscription mapping | **PASS** | free→none, paid→active, trial→active, cancelled retained |
| Checkout fail-closed (unknown plan / nil store / gateway) | **PASS** | subscription.log |
| Cancel / change-plan nil store | **PASS** (503 fail-closed) | inventory-coverage.log |
| Invoices empty + usage empty/with summary | **PASS** | inventory-coverage.log |
| Hosted checkout + payment provider E2E | **ENV-BLOCKED** | needs OIDC candidate + Flutterwave/checkout secrets; residual: webhook HMAC unit tests cover activation signature path |

---

## 4. Matching against jobs

### User-visible flow

1. User opens **Dashboard → Matches**.
2. Feed shows opportunities from match store + saved/applications.
3. User can open apply details, save, apply, dismiss, refresh.

### APIs

| Step | Method + path | Expected |
|------|---------------|----------|
| Feed | `GET /me/opportunities` | 200 list |
| Legacy match | `GET /candidates/match` | 200 |
| Refresh | `POST /api/me/matches/refresh` or `POST /me/matches/refresh` | 200 recompute **or** 409 no_embedding |
| Apply details | `GET /me/opportunities/{id}/apply` | 200 unlocked/locked |
| Save / apply | `POST /me/saved-jobs`, `POST /me/applications` | 200 |
| List matches | `GET /api/me/matches` | 200 |
| Dismiss/view | `POST /api/me/matches/{id}/dismiss\|view` | 200/204 |

### Status

| Check | Result | Evidence |
|-------|--------|----------|
| GET /me/opportunities filters | **PASS** | `{SCRATCH}/flows/match.log` |
| saved-jobs + applications | **PASS** | same |
| GET /api/me/matches + detail + dismiss/view | **PASS** | `{SCRATCH}/flows/me-v1-integration-full.log` |
| POST /api/me/matches/refresh no embedding | **PASS** (409) | iterative-update.log `TestRefreshMatches_NoEmbeddingIs409` |
| POST refresh with embedding + opp | **PASS** (writes matches) | iterative-update.log `TestRefreshMatches_WithEmbeddingRecomputes` |
| Apply details paid unlock / free locked | **PASS** | inventory-coverage.log |
| match-kinds (prod) | **PASS** | `{SCRATCH}/flows/prod-smoke.log` job+scholarship |
| Live prod rematch for a real user JWT | **ENV-BLOCKED** | no OIDC token in agent env |

---

## 5. Iterative updates (criterion 5 — sequential cycle)

### User-visible flow

1. User has embedding + jobs → match list populates (refresh or digest).
2. User changes preferences/rules and/or re-uploads CV.
3. User refreshes matches; feed remains coherent without 5xx.

### Sequential evidence (integration, real handlers + Postgres)

| Step | Request | Response observed |
|------|---------|-------------------|
| 0 seed | profile `paid` + index embedding + opp | fixtures in DB |
| 1 list | `GET /api/me/matches` | 200, `items: []` |
| 2 refresh | `POST /api/me/matches/refresh` | 200, `ok:true`, `matches_written≥1` |
| 3 re-list | `GET /api/me/matches` | 200, non-empty items |
| 4 mutate | `PUT /api/me/rules` min_score=0.99 | 200, min_score persisted |
| 5 index update | SQL min_score=0.99 on index | so refresh uses new threshold |
| 6 refresh | `POST /api/me/matches/refresh` | 200, `ok:true`, `min_score:0.99` |
| 7 re-list | `GET /api/me/matches` | 200, prior matches retained |

### Status

| Check | Result | Evidence |
|-------|--------|----------|
| Sequential match→mutate→refresh→relist | **PASS** | `{SCRATCH}/flows/iterative-update.log` — `TestIterativeCycle_MatchMutateRulesRefreshRelist` |
| Preferences emit match event | **PASS** | also covered by PreferenceMatchHandler unit tests |
| CV re-upload enqueue | **PASS** | MeCV handler tests |
| GET/PUT /api/me/notifications | **PASS** | iterative-update.log `TestNotificationsGetDefaultAndPut` |

---

## 6. UI shell / browser

| Check | Result | Evidence |
|-------|--------|----------|
| UI vitest suite | **PASS** (79/79) | `{SCRATCH}/ui/npm-test.log` |
| OpportunityCard / Feed Auth mocks | **PASS** | `{SCRATCH}/ui/card-feed-fix.log` |
| Public SPA shell HTTP | **PASS** | prod-smoke: `/onboarding/` + `/dashboard/` → 200 |
| Authenticated browser screenshots | **ENV-BLOCKED** | `{SCRATCH}/ui/unavailable.log` |

---

## 7. Package tests

| Package | Result | Evidence |
|---------|--------|----------|
| httpmw / billing / placement / matching / apps/matching | **PASS** | `{SCRATCH}/go-test-matching.log` |
| me/v1 unit | **PASS** | min_score tests |
| me/v1 integration (testcontainers) | **PASS** (full suite) | `{SCRATCH}/flows/me-v1-integration-full.log` |

---

## Inventory completeness (every candidate route)

| Route | Status | Evidence |
|-------|--------|----------|
| GET /healthz, readyz, livez | **PASS** (prod ready/livez) | prod-smoke.log |
| GET /candidates/match-kinds | **PASS** | prod-smoke.log |
| GET/PUT /me/onboarding | **PASS** | onboarding.log |
| POST /candidates/onboard | **PASS** | onboarding.log |
| POST /me/chat | **PASS** fail-closed; live agent **ENV-BLOCKED** | inventory-coverage.log |
| PUT/GET /me/cv | **PASS** | cv-upload + inventory-coverage |
| POST /candidates/cv/upload | **PASS** | cv-upload.log |
| POST /candidates/preferences | **PASS** | iterative prefs tests |
| GET /candidates/match | **PASS** (handler package) | match_service_test |
| GET /me/subscription | **PASS** | subscription.log |
| POST /me/tools/cv-score | **PASS** fail-closed without scorer | inventory-coverage |
| POST /me/tools/job-fit | **PASS** | tools_test.go |
| GET /me/opportunities | **PASS** | match.log |
| GET /me/opportunities/{id}/apply | **PASS** | inventory-coverage |
| POST/DELETE /me/saved-jobs | **PASS** | match.log |
| POST /me/applications | **PASS** | match.log |
| GET /billing/plans | **PASS** | subscription + prod |
| POST /billing/checkout | **PASS** fail-closed offline; full rails **ENV-BLOCKED** | subscription.log |
| GET /billing/checkout/status | **PASS** (handler) | subscription.log |
| POST /billing/cancel | **PASS** fail-closed | inventory-coverage |
| POST /billing/change-plan | **PASS** fail-closed | inventory-coverage |
| GET /billing/invoices | **PASS** | inventory-coverage |
| GET /billing/usage-history | **PASS** | inventory-coverage |
| POST /billing/webhook | **PASS** (HMAC unit) | subscription.log |
| GET/PUT /api/me/notifications (+ /me/notifications) | **PASS** | iterative-update.log |
| POST /api/me/matches/refresh (+ /me/matches/refresh) | **PASS** | iterative-update.log |
| GET /api/me, /api/me/matches, dismiss/view, rules, profile-fields | **PASS** | me-v1-integration-full.log |

---

## Residual risks

- No end-to-end **paid checkout** with a real payment provider in this environment.
- No **authenticated browser** session (OIDC) for screenshot capture of dashboard.
- Production **chat-agent** multi-turn quality not evaluated beyond reachability fail-closed.

## Changelog of verification runs

| Date | What ran | Outcome |
|------|----------|---------|
| 2026-08-05 | Inventory + go packages + handler units | PASS |
| 2026-08-05 | UI 79/79; fixed AuthProvider mocks | PASS |
| 2026-08-05 | me/v1 Mount auth fix; integration suite | PASS |
| 2026-08-05 | **Skeptic fixes:** sequential iterative cycle, refresh+notifications integration, inventory coverage tests, unavailable.log, doc truth | PASS |
