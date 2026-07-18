# Traction & product audit (capital-critical)

**Date:** 2026-07-17  
**Verdict:** Strong crawl/match infrastructure; **product surface over-promised and under-delivered**. Highest risk is trust: sell only what ships.

## Executive summary

Users pay for **ranked shortlists + digests + honest apply handoff**. They do not pay for vapor (auto-apply, interview prep, dedicated agent) or for a Managed plan that **hid the match feed**.

| Layer | Status |
|-------|--------|
| Crawl → PG → embeddings | Real |
| Reverse-KNN matching + digests | Real (scoring bug fixed this release) |
| Billing Flutterwave/checkout | Real (ledger persist hard-fail on error) |
| Dashboard match feed | Real for Starter; **Managed was blanked — fixed** |
| Apply | Was “mark applied” without opening employer — **fixed** |
| Auto-apply / interview prep / agent 1:1 | **Marketing only — copy removed** |

## Funnel scorecard

| Stage | Grade | Notes |
|-------|-------|-------|
| Land | Weak | Multi-kind “coming soon”, free CTA vs paid match |
| Browse | OK | Newest-first listings; jobs inventory is real |
| Signup / onboarding | Weak | Chat/CV good; hard paywall before proof of value |
| Matches | Fixed | Managed now sees same feed; empty-state guidance |
| Apply | Fixed | Opens `apply_url` then tracks |
| Pay | Weak–OK | Ledger failure no longer silent; value after pay is the conversion bet |
| Digests | Ops OK | Depends on service-notification templates + match quality |

## 2026-07-18 — Trust + free proof + tools

| Change | Detail |
|--------|--------|
| Free first matches | Match refresh no longer requires subscription (proof caps 1/day, 3/week) |
| Dashboard free access | Unpaid users use full dashboard; soft upgrade banner |
| Onboarding | Primary CTA: free matches → dashboard; subscribe optional |
| Tools section | Free CV ATS score + job fitness checker |
| Marketing honesty | Homepage, FAQ, pricing — jobs-first, no auto-apply/agent claims |

Recruiter/employer ATS posting is intentionally deferred.

## Shipped this cycle (P0)

1. **Managed matches UI** — full feed + auto-refresh (no agent theater).
2. **Honest apply** — open employer URL, then track; honest toasts.
3. **Honest pricing** — Starter/Managed features match reality; AutoApply entitlement false until automation exists.
4. **Scoring neutrality** — missing skills/geo/salary no longer zero Path A/C scores.
5. **Billing ledger** — Create failure after gateway create returns 502.
6. **Newest-first discovery** — posted_at default (v8.0.188).
7. **HTML descriptions** — consistent render (v8.0.187).

## Kill / park list

| Claim | Action |
|-------|--------|
| Auto applications | Park until ATS automation or staffed ops |
| Interview prep / coaching | Remove from marketing |
| Dedicated agent / weekly 1:1 | Park; AgentCard dead until assignment API |
| Multi-kind homepage first-class | Keep secondary until matchers + inventory live |
| “Free for seekers” = full product | Reframe: browse free, match paid |

## Next 30 days (priority)

| # | Item | Why |
|---|------|-----|
| 1 | Path A env required + metrics | Live matching for new jobs |
| 2 | Notification templates registered + Send metrics | Digests actually land |
| 3 | Post-pay first-match <60s + empty reasons API | Stops “paid empty” |
| 4 | Match dismiss in UI + reasons chips | Personalization loop |
| 5 | Await onboarding save before checkout | Profile ready before charge |
| 6 | Homepage jobs-first honesty | CAC quality |

## Competitive position

Win on **fresher, ranked, region-aware shortlists + digests**. Lose if competing on Easy Apply or free volume without better ranking. Do not fund Managed white-glove until ops exist.

## Production readiness (user-visible)

| Area | Status |
|------|--------|
| Billing paid-but-free | Improved (persist hard-fail) |
| Path A off by env | Still a config risk — require in prod |
| Scoring empty feeds | Improved (neutrality) |
| Notify silence | Config + template dependency remains |
| AUTH_REQUIRE_JWT default false | Prod must force true |

**Overall: READY WITH CONDITIONS** — ship Starter honesty; hold Managed complexity; fix Path A/notify prod config before scaling paid acquisition.
