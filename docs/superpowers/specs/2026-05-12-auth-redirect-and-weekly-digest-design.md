# Auth redirect, paid-user flow, and unpaid weekly digest

**Date:** 2026-05-12
**Status:** Approved (autonomous implementation authorised)

## Problem

Three coupled gaps in the candidate flow:

1. **Popup-based OIDC**. `@stawi/auth-runtime` opens a `window.open()`
   popup; browsers commonly block third-party popups, breaking sign-in
   silently on first click. The fallback path inside `runtime.ts`
   imports `oauth-popup.ts` — there is no redirect mode.
2. **Paid-user happy path unverified end-to-end**. Dashboard reads
   `sub.queued_matches`, `sub.delivered_this_week`, `sub.agent` from
   `/me/subscription`. Each component exists in isolation; nobody has
   walked pricing → checkout → status → first match → email
   end-to-end on staging since `service_payment` + `service_billing`
   were wired in.
3. **No re-engagement loop for the "signed up but didn't pay"
   segment**. Onboarding+payment is a two-step funnel and drop-off
   sits between them. The matches weekly digest only runs for
   `active` candidates, so the unpaid drop-offs go silent.

## Goals

- Sign-in is a single full-page redirect; no popup is opened anywhere
  in the runtime; no popup-blocker failure mode exists.
- A paid user can complete the entire flow on staging without
  intervention, and any gap surfaced is fixed inside this delivery.
- Every Monday at 09:00 UTC, every logged-in candidate without an
  active subscription receives an email with the top 10 freshest
  opportunities in their country + opted-in kinds, plus three
  headline analytics, plus a single CTA to pick a plan.

## Non-goals

- No new subscription tier. The "logged-in, not paid" segment remains
  the existing checkout-incomplete state; the digest does not promote
  it to a tier.
- No template work inside `service_notification` beyond declaring the
  payload shape and topic. Rendering belongs to that service.
- No FedCM changes — the silent FedCM probe stays as it is.

---

## Phase 1 — Popup → redirect

### Where the change lives

`/home/j/code/stawi.dev/widgets.js` — the upstream `@stawi/auth-runtime`
package. `@stawi/profile` consumes it unchanged. `stawi.jobs` only
bumps the dependency and updates `/auth/callback/`.

### Changes in `@stawi/auth-runtime`

- Delete `src/oauth-popup.ts`.
- Add `src/oauth-redirect.ts` with two functions:

  ```ts
  export function startRedirect(
    cfg: ResolvedConfig,
    core: WorkerCore,
  ): Promise<never>;
  export function completeRedirect(
    cfg: ResolvedConfig,
    core: WorkerCore,
  ): Promise<{ returnTo: string }>;
  ```

  `startRedirect` calls `core.prepareAuth()` → stashes
  `{ state, verifier, returnTo: location.pathname + location.search }`
  in `sessionStorage` under `stawi.auth.redirect.v1` → calls
  `window.location.assign(authUrl)` → returns a never-resolving
  promise (the page is navigating away). `completeRedirect` reads
  `?code` + `?state` from the current URL, pulls the stash, calls
  `core.completeAuth(...)`, clears the stash, returns the stored
  `returnTo`.

- `src/runtime.ts::ensureAuthenticated`: replace the dynamic
  `oauth-popup.js` import with a call to `startRedirect`. FedCM
  flow above it is unchanged.

- `src/index.ts`: export new `completeAuthCallback()` helper that
  builds a transient runtime, calls `completeRedirect`, destroys
  the runtime, and returns `{ returnTo }` for the caller to
  navigate to. The host page never directly touches the worker
  core — this single function is the entire public callback API.

- `src/shared/errors.ts`: remove `OAUTH_POPUP_BLOCKED`,
  `OAUTH_POPUP_CLOSED`, `OAUTH_POPUP_TIMEOUT`. Add
  `OAUTH_REDIRECT_STORAGE_MISSING`. Keep `OAUTH_STATE_MISMATCH`,
  `OAUTH_FAILED`.

- Tests: delete `oauth-popup.test.ts`. Add `oauth-redirect.test.ts`
  covering the four states (happy path, missing sessionStorage,
  state mismatch, token exchange error).

- Version: `1.1.0` for both `@stawi/auth-runtime` and `@stawi/profile`
  (profile re-publishes only to pin the new runtime).

### Changes in `stawi.jobs`

- `ui/app/package.json`: bump `@stawi/auth-runtime` and
  `@stawi/profile` to `^1.1.0`. Re-run `npm install`.
- `ui/app/src/components/AuthCallback.tsx`: on mount, call
  `completeAuthCallback({ idpBaseUrl, clientId, installationId,
apiBaseUrl, redirectUri })`; on success
  `window.location.assign("/dashboard/")`; on
  `OAUTH_REDIRECT_STORAGE_MISSING` show a "couldn't complete sign in"
  panel with a retry link back to `/`.
- `ui/layouts/auth/single.html`: drop the popup-handoff comment;
  the route is now first-class.
- No change needed to `StawiAuth.tsx` or `Dashboard.tsx` — they
  call into the widget which calls into the runtime, and the
  runtime's contract is unchanged from their perspective.

### Failure modes

| Failure                                                            | Detection                       | Behaviour                                                                  |
| ------------------------------------------------------------------ | ------------------------------- | -------------------------------------------------------------------------- |
| `sessionStorage` cleared by the user between redirect and callback | `completeRedirect` reads `null` | Throw `OAUTH_REDIRECT_STORAGE_MISSING`; `AuthCallback` renders retry panel |
| `state` returned by IdP ≠ stashed state                            | `core.completeAuth` rejects     | Throw `OAUTH_STATE_MISMATCH`; same retry panel                             |
| Network error during token exchange                                | `core.completeAuth` rejects     | Surface error; retry panel with the message                                |
| User hits browser-back from the IdP                                | URL has no `?code`              | `completeRedirect` rejects with `OAUTH_FAILED`; retry panel                |

### Testing

- Unit (vitest): `oauth-redirect.test.ts` with `happy-dom`; stub
  `window.location.assign`; assert sessionStorage shape; assert
  `completeRedirect` clears the stash on success even if
  `core.completeAuth` throws.
- Manual: deploy a preview, sign in from incognito, confirm no
  popup. Repeat with popup blocker maximally restrictive.

---

## Phase 2 — Paid-user flow audit + fixes

### Approach

A code-and-config walkthrough — not a Playwright suite — because the
failure modes here are integration mismatches (audience strings,
plan IDs, missing fields) rather than UI breakage, and a real test
account against staging is cheaper to run than a maintained E2E
harness.

### What gets walked

1. **`/pricing/` cards → `createCheckout`**. Verify
   `ui/layouts/pricing/single.html` posts the same `plan_id`
   strings that `apps/api/cmd/endpoints_v2.go` accepts. (Plan IDs
   are `starter|pro|managed` per `ui/app/src/utils/plans.ts`;
   confirm the API checks against the same set.)
2. **`createCheckout` → Polar/M-PESA/Airtel/MTN routing**. Verify
   `pkg/billing` picks the correct PSP from `CF-IPCountry` and
   returns either `redirect_url` (Polar) or `prompt_id`
   (MPESA/MTN/Airtel). Failure mode: missing country header on
   the request — fix by falling back to user's
   `preferred_countries[0]` or US-default.
3. **PSP webhook → `service_billing` subscription created**.
   Confirm the webhook handler updates `CandidateProfile.SubscriptionID`
   and `CandidateProfile.PlanID`. The bug-class to catch here is
   "webhook fires but the candidate row's subscription stays
   `free`" — a missing `UPDATE candidate_profiles`.
4. **`/me/subscription` returns the populated fields**. Walk the
   handler that builds `MeSubscription` and confirm
   `queued_matches`, `delivered_this_week`, `renews_at`, `agent`
   are all populated (not zero-defaulted) once a subscription is
   live. The dashboard depends on these.
5. **First match arrives**. Trigger
   `POST /_admin/matches/weekly_digest` against the test account
   manually, watch `candidates.matches.ready.v1` fire,
   confirm `service_notification` receives it and a matches email
   actually goes out.
6. **Dashboard tier-specific panels render**. With an `active`
   starter / pro / managed candidate respectively, confirm the
   correct surface renders (matches panel for starter+pro, agent
   card for managed, billing panel with renew date for all).

### Output of the audit

A short list of concrete bugs/gaps, each closed in this delivery
phase. We do not write a separate report; the fix commits speak for
themselves.

### Expected gaps (hypotheses to verify, not pre-confirmed)

- `MeSubscription.queued_matches` may be hard-coded to zero in the
  `/me/subscription` handler (we never grep'd for it during this
  spec).
- `MeSubscription.delivered_this_week` may not count from
  `candidate_matches` rows with `sent_at` within the last 7 days.
- `RetryCheckoutButton` in `Dashboard.tsx` redirects to
  `?billing=pending&prompt_id=…` for non-Polar routes, but no
  component reads that param and polls
  `pollCheckoutStatus(promptId)` — the user is stranded on the
  dashboard until they refresh.

If any of those hypotheses turn out true we fix them; if any
turn out false we move on.

### Testing

For each gap fixed:

- Unit test against the relevant handler.
- One manual round-trip on staging.

---

## Phase 3 — Weekly digest for unpaid logged-in users

### Audience

Every `CandidateProfile` where
`Subscription IN ('free','trial','cancelled')` OR `SubscriptionID = ''`.
Paid users (`Subscription = 'paid'` with a non-empty `SubscriptionID`)
are excluded — they get the matches digest instead.

### Email payload

- **Top 10 jobs**: from canonical job rows where
  `first_seen_at >= now - 7d`, filtered by the candidate's
  `preferred_countries` (fallback: their `country` from the profile)
  AND `kind IN candidate's opted-in kinds` (fallback: `['job']`).
  Ordered by `first_seen_at DESC`. Each item carries: title,
  company, country, kind, slug (so the notification service can
  build the canonical URL), `first_seen_at`.
- **Headline stats** (computed once per digest run, not per
  candidate — same global numbers, but with the candidate's
  country narrowing applied for the country-specific stat):
  - `total_new_jobs_this_week` (global, since we don't yet have a
    per-country count cheap to compute in Manticore without a
    facet round-trip per candidate).
  - `top_countries`: top 3 country ISO codes by new-job count.
  - `top_kinds`: top 3 of `job|scholarship|tender|deal|funding`
    by new-job count.
- **CTA**: a single `pick_a_plan_url` pointing at `/pricing/`.

### Architecture

Following the existing `matches_weekly` + `cv_stale_nudge` patterns:

- **New event topic**: `candidates.weekly_jobs_digest.v1` in
  `pkg/events/v1/names.go`. Notification-only (like
  `TopicCandidateCVStaleNudge`) — intentionally absent from
  `AllTopics()`.
- **Envelope payload type** in
  `pkg/events/v1/candidates.go`:

  ```go
  type WeeklyJobsDigestV1 struct {
      CandidateID string         `json:"candidate_id"`
      Country     string         `json:"country"`
      Locale      string         `json:"locale"`
      Jobs        []DigestJob    `json:"jobs"`
      Stats       DigestStats    `json:"stats"`
      PlansURL    string         `json:"plans_url"`
  }
  type DigestJob struct {
      CanonicalID  string    `json:"canonical_id"`
      Title        string    `json:"title"`
      Company      string    `json:"company"`
      Country      string    `json:"country"`
      Kind         string    `json:"kind"`
      Slug         string    `json:"slug"`
      FirstSeenAt  time.Time `json:"first_seen_at"`
  }
  type DigestStats struct {
      TotalNewThisWeek int                `json:"total_new_this_week"`
      TopCountries     []KindCountryStat  `json:"top_countries"`
      TopKinds         []KindCountryStat  `json:"top_kinds"`
  }
  type KindCountryStat struct { Code string `json:"code"`; Count int `json:"count"` }
  ```

- **New admin handler** at
  `apps/matching/service/admin/v1/weekly_jobs_digest.go` exposing:
  - `UnpaidCandidateLister.ListUnpaidWithEmail(ctx) ([]UnpaidCandidate, error)`
    where `UnpaidCandidate` carries id, country, locale, opt-in
    kinds.
  - `NewJobsLister.ListNewJobs(ctx, country, kinds, limit, since) ([]DigestJob, error)`
    backed by Manticore (`SELECT * FROM jobs_index WHERE first_seen_at > ? AND country = ? AND kind IN (...) ORDER BY first_seen_at DESC LIMIT 10`).
  - `WeeklyStatsLister.GlobalStats(ctx, since) (DigestStats, error)`
    backed by Manticore facets.
  - `WeeklyJobsDigestHandler` orchestrating the three, emitting
    one envelope per candidate.

- **Registered in `apps/matching/cmd/main.go`** alongside the
  existing `_admin` routes:

  ```go
  mux.HandleFunc("POST /_admin/candidates/weekly_jobs_digest",
      adminv1.WeeklyJobsDigestHandler(adminv1.WeeklyJobsDigestDeps{
          Svc: svc, Lister: unpaidLister,
          Jobs: newJobsLister, Stats: weeklyStatsLister,
      }))
  ```

- **Trustage workflow** at
  `definitions/trustage/candidates-weekly-jobs-digest.json` —
  cron `0 8 * * 1` (08:00 UTC, an hour earlier than matches digest
  so the unpaid users get re-engaged before the paid matches go
  out). Calls `POST http://opportunities-candidates.product-opportunities.svc/_admin/candidates/weekly_jobs_digest`.

- **Repository / data access**:
  - `CandidateRepository` gains a `ListUnpaid(ctx) iter` method
    that streams id/country/locale/opt-in-kinds. Opt-in kinds are
    derived from existing preferences columns (`PreferredRegions`
    is the wrong field — we need a new `EnabledKinds` column or
    derive from the existence of per-kind opt-in records). **If a
    dedicated column doesn't exist**, we infer from
    `target_job_title != ''` → `'job'`, and add a TODO to ship a
    proper opt-in store with the next preference-store revision.
  - Manticore facet queries reuse the same client constructed at
    boot in `apps/matching/cmd/main.go`.

- **Notification service contract**: we own the topic +
  payload shape only. `service_notification` ships the template
  (matches the existing pattern of cv-stale-nudge and
  candidates.matches.ready.v1).

### Failure modes

| Failure                                          | Behaviour                                                                                                         |
| ------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------- |
| `ListUnpaid` returns 0 rows                      | Handler returns `{ok:true,emitted:0}`; Trustage logs a success                                                    |
| Manticore unavailable for the global stats query | Skip the stats block; emit envelopes with `stats: {}`; downstream renders the email without the analytics section |
| Per-candidate new-jobs query fails               | Log+skip that candidate; continue the sweep                                                                       |
| Event emit fails                                 | Increment `Failed`; same as the matches-weekly handler                                                            |

### Testing

- Unit: fake `UnpaidCandidateLister`, `NewJobsLister`, `WeeklyStatsLister`
  with `EventsManager` fake; assert one envelope emitted per unpaid
  candidate with the right country/kinds filtering, stats block on
  every envelope, skip-on-failure for one candidate doesn't abort
  the sweep.
- Integration (existing testcontainers Postgres + Manticore in
  `tests/`): seed 50 unpaid + 5 paid candidates, 100 new jobs across
  3 countries, run the handler, assert paid candidates skipped and
  unpaid candidates receive country-filtered top-10.
- Smoke on staging: trigger the workflow manually via Trustage,
  confirm `service_notification` logs receipt of the envelopes,
  confirm one test inbox receives the email.

---

## Sequencing

| Phase | PR                                                   | Depends on                              |
| ----- | ---------------------------------------------------- | --------------------------------------- |
| 1     | widgets.js `auth-runtime@1.1.0` + `profile@1.1.0`    | —                                       |
| 1     | stawi.jobs bump + AuthCallback rewrite               | widgets.js publish                      |
| 2     | Audit findings + targeted fixes                      | none (can run in parallel with Phase 1) |
| 3     | matching: handler + repo + topic + Trustage workflow | none (can run in parallel with Phase 1) |

Phases 2 and 3 can land before Phase 1 lands — they don't touch
auth.

## Verification

End of all three phases:

- Sign-in from incognito in Chrome / Firefox / Safari with popup
  blocker maxed: no popup, full-page redirect, lands on
  `/dashboard/`.
- Test paid candidate completes pricing → checkout → first match
  email round-trip on staging with no manual intervention.
- Test unpaid candidate (subscription empty, completed onboarding)
  receives one digest email at the next Monday 08:00 UTC run.

## Out of scope (explicit list)

- Refactoring `pkg/billing` PSP routing.
- Switching the `MeSubscription.queued_matches` field to read from
  a live counter rather than a periodic refresh (the audit fixes
  whatever's broken; it doesn't redesign the metric).
- Localising the digest email body — `service_notification` handles
  i18n at template render time; the payload carries `Locale` already.
- A "weekly digest" preference toggle on `/dashboard/`. Unpaid
  users implicitly receive it; if we add a toggle later it lives
  on the same preferences endpoint.
