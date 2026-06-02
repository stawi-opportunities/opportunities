# Paid-Flow Routing, Resumable Onboarding & Unified Opportunities Feed

**Status**: draft for review
**Authors**: agent + reviewer
**Date**: 2026-05-23

---

## Summary

After sign-in, a candidate should land on exactly one surface:

- **`/dashboard/`** if their subscription is `active` — a single feed of opportunities (matches + starred + applications, with state inline on each card).
- **`/onboarding/`** in every other case — a 3-step wizard that resumes from the step the candidate last completed.

Today the gate sort-of exists (`Dashboard.tsx` shows a `CompletePaymentPanel` for inactive subs) but `AuthCallback` hard-codes `/dashboard/`, the wizard is in-memory only (refresh wipes progress), and the dashboard's matches / saved / applications panels are placeholders that don't fetch real data. This spec turns those three threads into a coherent, production-grade flow.

## Goals

- **Single canonical gate**: payment is the only thing that decides where a signed-in candidate lands.
- **Resumable wizard**: a candidate who walks away mid-onboarding picks up at exactly the step they left, on any device, with their answers intact.
- **Real, unified opportunities feed**: one list, one fetch, with inline star + application status per card; URL-driven filter chips (`?filter=…`) for shareable + back-button-friendly state.
- **Production-grade defaults**: graceful degradation when subscription / draft / opportunities endpoints fail; no infinite spinners, no console-only errors.

## Non-goals

- Redesigning the onboarding wizard's _content_ (steps, copy, fields) — this spec preserves it.
- Building a _new_ matching/saved/applications backend. Those exist (`pkg/matching`, `pkg/applications`, `/saved-jobs/*` routes); this spec wires them up.
- Multi-version onboarding flows or A/B-tested wizards. One canonical wizard for now.
- Profile-completeness gating (Q1 settled: payment is the only gate). CV upload and profile edits happen _inside_ the dashboard.

## Decisions made during brainstorming

| Decision                     | Choice                                                  | Why                                                                                                                                                                                       |
| ---------------------------- | ------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Gating logic                 | Payment-only gate                                       | Simplest mental model; existing `CompletePaymentPanel` already encodes this implicitly. Profile completion is non-blocking.                                                               |
| Onboarding draft persistence | Server-side                                             | Paying-tier product; users expect cross-device resume. localStorage would break the value proposition.                                                                                    |
| Dashboard IA                 | Unified feed with inline state                          | One canonical list of "what's relevant to me right now". Star and application status are properties of an opportunity, not separate domains.                                              |
| Draft storage shape          | `onboarding_draft JSONB` column on `candidate_profiles` | Lighter migration, no join on read, schemaless tolerates wizard evolution. Distinct from the "committed" columns (`target_job_title`, etc.) which are populated only by the final submit. |

---

## Architecture

```
                   ┌────────────────────────┐
                   │ /auth/callback/        │
                   │ AuthCallback.tsx       │
                   │ 1. completeRedirect()  │
                   │ 2. GET /me/subscription│
                   │ 3. route               │
                   └─────────┬──────────────┘
                             │
                ┌────────────┴────────────┐
                │                         │
   sub.status === "active"        sub.status !== "active"
                │                         │
                ▼                         ▼
       /dashboard/                  /onboarding/
       (page-level guard            (page-level guard
        redirects unpaid             redirects paid users
        users to /onboarding/)       to /dashboard/)
                │                         │
                ▼                         ▼
       OpportunitiesFeed         GET /matching/me/onboarding
       GET /matching/me/         → { step, fields }
         opportunities           render Step{N}Form pre-filled
       + Star/Apply actions      PUT /matching/me/onboarding on each Next
```

The same gate logic runs in three places (AuthCallback after redirect, `/dashboard/` page load, `/onboarding/` page load). It's the same single source of truth: `fetchMeSubscription()`. Page-level guards mean direct-URL bookmarks (a user opening `/dashboard/` from history while still unpaid) self-correct rather than rendering a broken state.

---

## Components

### 1. `AuthCallback` routing decision

`ui/app/src/components/AuthCallback.tsx` currently:

```tsx
rt.completeRedirect().then(() => window.location.assign("/dashboard/"));
```

After:

```tsx
rt.completeRedirect().then(async () => {
  const sub = await fetchMeSubscription();
  const target = sub.status === "active" ? "/dashboard/" : "/onboarding/";
  window.location.assign(target);
});
```

`fetchMeSubscription` already exists and already has a try/catch fallback (returns `{ status: "none", ... }` on error) — so a wedged matching service degrades to "send the user to onboarding", which is the safer default for an inactive-or-unknown subscription. The redirect goes via `window.location.assign` so the destination page mounts cleanly with a fresh React tree.

### 2. Page-level guards

Both `Dashboard.tsx` and `Onboarding.tsx` already check `state === "authenticated"` via `useAuth()`. Add a second `useEffect` that runs once the auth state settles and the subscription has been fetched:

```ts
// In Dashboard.tsx
useEffect(() => {
  if (state !== "authenticated") return;
  if (subQ.isLoading) return;
  if (subQ.data?.status !== "active") {
    window.location.assign("/onboarding/");
  }
}, [state, subQ.isLoading, subQ.data?.status]);

// In Onboarding.tsx, the mirror:
useEffect(() => {
  if (state !== "authenticated") return;
  if (subQ.isLoading) return;
  if (subQ.data?.status === "active") {
    window.location.assign("/dashboard/");
  }
}, [state, subQ.isLoading, subQ.data?.status]);
```

The brief "wrong-page render" before redirect is masked by the existing `Skeleton` loading state. No flash because the redirect happens during the same effect that the data-fetch resolves into.

### 3. Onboarding draft persistence

#### Data model

Add one column to `candidate_profiles`:

```sql
ALTER TABLE candidate_profiles
  ADD COLUMN onboarding_draft JSONB NOT NULL DEFAULT '{}'::jsonb;
```

The draft object is opaque to the database; the wizard owns its schema. Today it looks like:

```json
{
  "step": 2,
  "fields": {
    "target_job_title": "Backend Engineer",
    "experience_level": "mid",
    "job_search_status": "actively_looking",
    "preferred_regions": ["Africa"],
    "preferred_timezones": ["UTC+0", "UTC+1"],
    "preferred_languages": ["English"],
    "job_types": ["Full-time"],
    "country": "KE"
  },
  "updated_at": "2026-05-23T14:02:11Z"
}
```

`step` is the _next_ step the user should land on. `updated_at` is informational (so we can sort drafts when debugging or expire them later if we want). The `fields` shape mirrors the `FormValues` type in `Onboarding.tsx` minus secret/file fields (`cv`, `agreeTerms`).

A new (or re-onboarding) candidate has `onboarding_draft = '{}'::jsonb`, which the backend serves as `{ step: 1, fields: {} }`. After `POST /candidates/onboard` succeeds, the draft column gets cleared back to `'{}'::jsonb` in the same transaction — the canonical profile fields now hold the data.

#### Endpoints

Both live on `service-opportunities-matching` under the `/me/*` namespace (already routed via `/matching/me/*` at the gateway). Both wrap with `httpmw.CandidateAuth`.

```
GET /me/onboarding
→ 200 application/json
  {
    "step": 1 | 2 | 3,
    "fields": { ...wizard form fields... },
    "updated_at": "RFC3339 timestamp"
  }

PUT /me/onboarding
← application/json
  {
    "step": 1 | 2 | 3,
    "fields": { ...partial set... }
  }
→ 204 No Content
```

`PUT` is a full-document replace of the draft object. The wizard always sends the _current_ state (step + the accumulated fields it has so far), so race conditions across tabs degrade to "last write wins" — acceptable for a draft.

Both endpoints are idempotent for the user's own draft, so `Idempotency-Key` is not required — but the existing `httpmw.Idempotency` middleware can wrap the PUT to dedupe rapid double-clicks of "Next".

#### Wizard flow

```
Mount
  ↓
GET /matching/me/onboarding
  ↓
draft = { step: N, fields: {...} }
  ↓
form.reset(fields)
setStep(N)
  ↓
User edits step N → Next
  ↓
validate step N's slice
PUT /matching/me/onboarding { step: N+1, fields: {...accumulated...} }
setStep(N+1)
  ↓
... same on step N+1 ...
  ↓
On the final step (3) Next → POST /matching/candidates/onboard (existing endpoint)
  ↓
server clears onboarding_draft
  ↓
createCheckout(...) → redirect to payment provider
```

If `PUT` fails mid-wizard, the UI shows a non-blocking inline warning ("Couldn't save — we'll retry") and keeps the user on the current step so no work is lost client-side. The Next button stays enabled; the next click triggers a fresh save.

### 4. Unified opportunities feed

Replace `MatchesPanel` / `SavedJobsPanel` / `ApplicationsPanel` (placeholder copy today) with one `OpportunitiesFeed` component on the dashboard.

#### Component shape

```
<OpportunitiesFeed>
  <Filters>                              // chips: All · Matches · Starred · Applied
  </Filters>
  <ul>
    {items.map(o => <OpportunityCard key={o.opportunity_id} ... />)}
  </ul>
  <LoadMore />                           // cursor-based, "Load more" button
</OpportunitiesFeed>

<OpportunityCard>
  - title, company, location, posted_at, salary band
  - inline: score badge (if matched), star icon (toggle saved), application status pill
  - actions: Apply button (if not yet applied), View details
</OpportunityCard>
```

Filter chips are URL-driven: clicking "Starred" pushes `?filter=starred`, the back button restores `All`. The component reads the filter from `useSearchParams`-equivalent (we don't use React Router; read directly from `window.location.search`, push via `history.pushState`).

#### Endpoint

One new endpoint on the matching service:

```
GET /me/opportunities?filter=all|matches|starred|applied&cursor=<opaque>&limit=20
→ 200 application/json
  {
    "items": [
      {
        "opportunity_id": "opp_…",
        "snapshot": { title, company, location, posted_at, salary_min, salary_max, currency, kind, … },
        "score": 0.82,            // optional; present for matches
        "starred": true,          // always present
        "application": {           // optional; present when the user applied
          "status": "applied" | "responded" | "interview" | "offer" | "rejected" | "hired",
          "applied_at": "RFC3339",
          "last_event_at": "RFC3339",
          "method": "auto" | "manual"
        }
      },
      ...
    ],
    "next_cursor": "<opaque>" | null
  }
```

The handler joins (in one query):

- `candidate_matches` for score + match presence
- `candidate_saved_jobs` for starred state
- `candidate_applications` for application + status

`filter=all` returns the union; the other filters scope to the relevant join. Pagination via opaque cursor (the existing `pageCursor` pattern in `pkg/matching/store.go`).

#### Mutating actions

Star / unstar:

```
POST   /matching/me/saved-jobs       { opportunity_id }   → 201
DELETE /matching/me/saved-jobs/{id}                       → 204
```

Apply (manual / on-demand):

```
POST /matching/me/applications  { opportunity_id, method: "manual" } → 201 { application_id, status }
```

**Reality check on what's already shipped vs. what this spec has to build**:

- **Saved jobs**: nothing exists. No table, no repository, no handler. The `/saved-jobs` HTTPRoute prefix on `opportunities-matching` was reserved (and later moved under `/matching/saved-jobs` in the prefix-rename) but no code backs it. This spec adds: a `candidate_saved_jobs` table, a `SavedJobsStore` in `pkg/savedjobs/`, and `POST`/`DELETE`/list handlers on `apps/matching`.

- **Applications**: backend code exists in `apps/applications/` and `pkg/applications/` (shipped in commit `bd410e0`, "applications phase 3"). HTTP CRUD lives at `/api/me/applications/*` on its own binary. But the service is **not deployed to the cluster** — no HelmRelease under `namespaces/product-opportunities/`, no HTTPRoute, no image policy. To make application state visible on the dashboard, we either (a) ship the applications service per the convention (Helm release + `/applications/*` PathPrefix), or (b) read `candidate_applications` directly from the matching service since both services already share the same Postgres database (`db/migrations/0010_applications_oltp.sql` lives in this repo and is applied by every service's migration job). This spec picks **(b) for now** — direct read from the matching service is the smallest change that unblocks the dashboard, and we defer shipping `apps/applications` as a deployed service to a follow-up. The applications _write_ path on the dashboard (the manual-apply button) calls the matching service, which inserts into `candidate_applications` using `pkg/applications/business/...` for the state-machine rules; the same business layer the unshipped applications service would use, just imported into matching for now.

- **Matches**: backend exists (`pkg/matching/store.go`). No public list endpoint yet — only the Phase-4 `/api/me/matches` under the unreachable `/api/` prefix. This spec adds the public list path implicitly via `GET /me/opportunities`.

### 5. Edge cases & failure modes

| Scenario                                                                        | Behaviour                                                                                                                                                                                                                                                                                                                                                     |
| ------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| User pays, returns minutes later → sub is `active` but their draft still exists | Gate routes them to `/dashboard/`. Draft is irrelevant. The `POST /candidates/onboard` finalisation step clears the draft in the same transaction, so this only happens if the user paid via direct checkout without going through the wizard's Step 3. We clear the draft after first dashboard load as a safety net.                                        |
| Payment status `pending` (M-PESA / Polar async)                                 | Existing `PendingCheckoutPoller` in `Dashboard.tsx` handles this — but the gate routes them to `/onboarding/` (sub isn't active yet). Onboarding shows a "Payment in progress — we'll move you to the dashboard automatically" banner instead of jumping back to Step 1. Banner reads `?billing=pending&prompt_id=…` from URL, same polling cadence as today. |
| User cancels subscription                                                       | Sub becomes `cancelled`; gate routes them back to `/onboarding/`. Their draft is empty (cleared at original onboard time), so the wizard starts fresh at Step 1 — they can re-onboard with a different plan.                                                                                                                                                  |
| Subscription expires mid-session                                                | `useQuery` refetches on focus / window mount; when the next fetch returns `cancelled` or `past_due`, the page-level guard kicks them to `/onboarding/`. We don't add aggressive interval polling — focus-refetch is plenty.                                                                                                                                   |
| `GET /me/onboarding` fails                                                      | Wizard falls back to `{step:1, fields:{}}` — empty form, user starts from the top. Non-blocking.                                                                                                                                                                                                                                                              |
| `PUT /me/onboarding` fails                                                      | Inline warning ("Couldn't save — we'll retry on next step"); button stays enabled; current-step state lives in `react-hook-form` regardless, so no work is lost client-side.                                                                                                                                                                                  |
| `GET /me/opportunities` fails                                                   | Feed shows a graceful empty-state with a Retry button (mirrors the existing `MatchesPanel` `subQueryError` branch). Other dashboard panels (billing, preferences) stay independent and keep rendering.                                                                                                                                                        |
| Stale draft from a previous wizard version                                      | The wizard validates the loaded `fields` against the current schema; unknown fields are dropped, known fields with invalid values fall back to defaults. No hard fail.                                                                                                                                                                                        |
| Two browser tabs open at the same step                                          | Both PUTs land on the server in some order; the last write wins. The losing tab will show stale values on next manual refresh — acceptable for a draft.                                                                                                                                                                                                       |

---

## Frontend changes

| File                                                  | Change                                                                                                                                                                   |
| ----------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `ui/app/src/components/AuthCallback.tsx`              | After `completeRedirect()`, fetch subscription and route to `/dashboard/` or `/onboarding/`.                                                                             |
| `ui/app/src/pages/Onboarding.tsx`                     | Mount: `GET /matching/me/onboarding`, hydrate form + step. Each Next: `PUT /matching/me/onboarding`. Add page-level guard redirecting active-sub users to `/dashboard/`. |
| `ui/app/src/pages/Dashboard.tsx`                      | Add page-level guard redirecting inactive-sub users to `/onboarding/`. Replace MatchesPanel + SavedJobsPanel + ApplicationsPanel with one `OpportunitiesFeed`.           |
| **NEW** `ui/app/src/components/OpportunitiesFeed.tsx` | The unified feed component. Owns filter chip state, pagination, mutating actions.                                                                                        |
| **NEW** `ui/app/src/components/OpportunityCard.tsx`   | Single card row with all inline state.                                                                                                                                   |
| `ui/app/src/api/candidates.ts`                        | Add `fetchOnboardingDraft`, `saveOnboardingDraft`, `fetchOpportunities`, `starOpportunity`, `unstarOpportunity`, `applyToOpportunity`.                                   |

The remaining panels — Preferences, Billing — stay as-is. The Profile widget (separate concern) stays in the sidebar.

## Backend changes

| Area                                                       | Change                                                                                                                                                                                                                           |
| ---------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Migration                                                  | `ALTER TABLE candidate_profiles ADD COLUMN onboarding_draft JSONB NOT NULL DEFAULT '{}'::jsonb`                                                                                                                                  |
| Migration (NEW table)                                      | `CREATE TABLE candidate_saved_jobs (candidate_id text, opportunity_id text, created_at timestamptz default now(), PRIMARY KEY (candidate_id, opportunity_id))` plus index on `candidate_id` for the join in `/me/opportunities`. |
| `apps/matching/service/http/v1/me_onboarding.go` (NEW)     | `GET` + `PUT` handlers.                                                                                                                                                                                                          |
| `apps/matching/service/http/v1/me_opportunities.go` (NEW)  | `GET /me/opportunities` with filter + cursor.                                                                                                                                                                                    |
| `apps/matching/service/http/v1/me_saved_jobs.go` (NEW)     | `POST` star and `DELETE` unstar handlers.                                                                                                                                                                                        |
| `apps/matching/service/http/v1/me_applications.go` (NEW)   | `POST` manual-apply handler. Wraps `pkg/applications/business` for the state-machine rules; same business code the not-yet-deployed `apps/applications` service uses.                                                            |
| `pkg/savedjobs/store.go` (NEW)                             | `Store` with `Star(ctx, candidateID, opportunityID) error`, `Unstar(ctx, candidateID, opportunityID) error`, plus helpers used by the aggregation query.                                                                         |
| `pkg/repository/candidate.go`                              | `GetOnboardingDraft(ctx, candidateID) ([]byte, error)`, `SetOnboardingDraft(ctx, candidateID, draft []byte) error`, `ClearOnboardingDraft(ctx, candidateID) error`.                                                              |
| `pkg/matching/store.go`                                    | New `ListOpportunitiesForCandidate(ctx, p ListOpportunitiesParams)` doing the 3-way join across `candidate_matches`, `candidate_saved_jobs`, `candidate_applications` (all same DB).                                             |
| `pkg/applications/business/onboard.go` (existing — extend) | On successful `POST /candidates/onboard`, clear the draft in the same transaction.                                                                                                                                               |
| `apps/matching/cmd/main.go`                                | Register the four new HTTP handlers alongside existing `/me/subscription`.                                                                                                                                                       |

## Testing strategy

| Layer           | Test                                                                                                                                                                                                               |
| --------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Unit (backend)  | `pkg/matching/store_test.go` integration tests for `ListOpportunitiesForCandidate` covering each filter + cursor pagination + empty case.                                                                          |
| Unit (backend)  | `pkg/repository/candidate_test.go` for GetOnboardingDraft / SetOnboardingDraft / ClearOnboardingDraft round-trip.                                                                                                  |
| Unit (backend)  | `apps/matching/service/http/v1/me_onboarding_test.go` + `me_opportunities_test.go` mirroring the `me_subscription_test.go` pattern (in-memory fakes for repos, full handler invocation, problem+json error paths). |
| Unit (frontend) | `OpportunitiesFeed` renders filter chips, calls fetch with right params, handles empty + error states. `OpportunityCard` star toggle, apply button optimistic-update + rollback on failure.                        |
| Integration     | Test that finalised submit clears the draft in the same transaction (write a draft, hit `POST /candidates/onboard`, expect draft column == `'{}'::jsonb`).                                                         |
| E2E (manual)    | Sign in fresh → land on `/onboarding/`. Fill step 1 → close tab. Re-sign-in → wizard resumes at step 2 with step 1 data pre-filled. Complete wizard + pay → land on `/dashboard/` showing real matches.            |

## Rollout

Three commits / three Flux reconciles, each independently shippable:

**Phase 1 — Backend foundations** (one PR on `stawi-opportunities/opportunities`, one image tag)

- Migration adding `onboarding_draft` column
- `GET` / `PUT /me/onboarding` handlers
- `pkg/repository/candidate.go` draft methods
- Tests
- AuthCallback frontend change still going to `/dashboard/`; gates not enforced yet. Nothing visibly changes for users.

**Phase 2 — Resumable wizard** (one PR on `stawi-opportunities/opportunities`)

- `Onboarding.tsx` loads + saves draft
- `AuthCallback` routes based on subscription
- Page-level guards on both pages
- Tests
- Users now experience resumable onboarding + correct routing. Dashboard still shows placeholder panels.

**Phase 3 — Saved-jobs backend + unified opportunities feed** (two PRs on `stawi-opportunities/opportunities`)

_Phase 3a (backend)_:

- Migration adding `candidate_saved_jobs` table + index
- `pkg/savedjobs/` store + tests
- `POST/DELETE /me/saved-jobs/...` handlers
- `pkg/matching/store.go` `ListOpportunitiesForCandidate` (3-way join across matches + saved_jobs + applications)
- `GET /me/opportunities` handler with filter + cursor
- `POST /me/applications` handler (wraps `pkg/applications/business` since the applications service isn't deployed yet — see "Reality check" above)
- Tests at every layer

_Phase 3b (frontend)_:

- `OpportunitiesFeed` + `OpportunityCard` components
- Replaces `MatchesPanel`, `SavedJobsPanel`, `ApplicationsPanel` in `Dashboard.tsx`
- Star + apply optimistic-update wiring
- URL-driven filter state
- Tests

Each phase is independently shippable — if Phase 3 has a problem, Phases 1+2 still deliver the resumable wizard value. Phase 3 can be paused after 3a to validate the backend before shipping the UI.

**Phase 4 (follow-up, not part of this spec)** — Deploy `apps/applications` as a first-class service per the api-gateway-path-prefix-convention: Helm release under `namespaces/product-opportunities/applications/`, HTTPRoute exposing `/applications/*`, image policy + automation. At that point the `POST /me/applications` handler on `apps/matching` becomes a thin proxy to the applications service (or gets retired and the dashboard calls `/applications/*` directly).

## Open questions

None blocking. Field-coverage caveat: `/public/user/info` doesn't ship `language` / `country` for the profile widget on first paint (already known; tracked elsewhere). Not blocking this work — the wizard collects country in Step 2 either way.

## Definition of done

- A signed-in candidate with no subscription is always routed to `/onboarding/`, never sees the dashboard's empty matches state.
- A signed-in candidate with an active subscription is always routed to `/dashboard/`, never re-fills the wizard.
- Closing the browser mid-wizard, signing back in from a different device, returns the candidate to exactly the step they were on with their previous answers pre-filled.
- The dashboard's opportunities feed renders real matches (with score), saved-flag state from `candidate_saved_jobs`, and application status from `candidate_applications`. Filter chips work and survive page reload via URL state.
- Every failure mode in the table above renders a non-blocking UI state (banner, retry button, fallback) — no infinite spinners, no white screens.
- Tests pass at every layer (`go test -race ./...`, `npm test`, `npm run typecheck`).
