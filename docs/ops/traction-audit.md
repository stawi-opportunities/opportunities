# Traction & product audit (capital-critical)

**Date:** 2026-07-18  
**Verdict:** **READY WITH CONDITIONS** for Starter acquisition. Free proof + tools + honest caps/diagnostics make the product usable before pay. Scale paid ads only after Path A + notify + JWT prod config are green.

**Full end-user value proof:** [end-user-value-proof.md](./end-user-value-proof.md) (journeys, trust table, automated verification, UAT checklist).

## Executive summary

Users pay for **ranked shortlists + digests + honest apply handoff**. They do not pay for vapor (auto-apply, interview prep, dedicated agent theater).

| Layer | Status |
|-------|--------|
| Crawl → PG → embeddings | Real |
| Reverse-KNN matching + digests | Real |
| Free proof matches (1/day, 3/week) | Real — weekly remaining enforced |
| Match dismiss + feed match_id | Real (v8.0.191) |
| Vector+keyword job-fit tools | Real (v8.0.191+) |
| Refresh empty reasons | Real (this release) |
| Billing Flutterwave/checkout | Real (ledger persist hard-fail) |
| Apply | Opens employer URL then tracks |
| Auto-apply / interview prep / 1:1 agent | **Not offered** — marketing honest |

## Funnel scorecard

| Stage | Grade | Notes |
|-------|-------|-------|
| Land | OK | Jobs-first, free first matches, no vapor CTAs |
| Browse | OK | Newest-first listings |
| Signup / onboarding | OK | Free matches CTA; subscribe optional |
| Matches | OK | Free + paid same feed; dismiss; diagnostic reasons |
| Tools | OK | Free CV ATS + vector job-fit |
| Apply | OK | Employer link + track |
| Pay | OK | Honest Starter/Managed value |
| Digests | Ops-dependent | Templates + service-notification must be live |

## Why it makes sense to use

1. **Browse free** — real inventory, no account required to search.
2. **Proof before pay** — capped shortlist + free tools after CV.
3. **Honest empty states** — weekly/daily cap, no inventory, below threshold — not silent blanks.
4. **Paid upgrade is clear** — more weekly matches, digests, priority alerts (Managed).
5. **Apply is real** — open employer site; we track, we don't fake success.

## Shipped this cycle

| Release | What |
|---------|------|
| v8.0.190 | Free proof, tools, honest marketing, dashboard free access |
| v8.0.191 | match_id dismiss UI, vector job-fit |
| v8.0.192 (this) | Honest weekly caps, refresh `reason` diagnostics, free Stats/Overview value, AgentCard honesty, score clamp |

## Production readiness

| Area | Status | Condition |
|------|--------|-----------|
| Weekly/daily caps | Enforced | Wire DailyCap + WeekCount on gap-fill (done) |
| Auth | Config risk | **Prod must set `AUTH_REQUIRE_JWT=true`** with OIDC |
| Path A fan-out | Config risk | Require queue + consumer in prod |
| Notify digests | Ops | Templates registered in service-notification |
| Matching extension | Default on | `MATCHING_EXTENSION_ENABLED=true` (default) |
| Billing ledger | Hard-fail | Create errors surface 502 |
| Dependabot vulns | Open | Track separately; not product-blocking |

### AUTH_REQUIRE_JWT

```
# production matching service
AUTH_REQUIRE_JWT=true
# plus valid OIDC / authenticator config
```

Without this, `/me/*` accepts spoofable `X-Candidate-ID` when no JWT authenticator is configured.

## Kill / park list

| Claim | Action |
|-------|--------|
| Auto applications | Park — AutoApply entitlement stays false |
| Interview prep / coaching | Not marketed |
| Dedicated agent weekly 1:1 | Not marketed; AgentCard is email support only if assigned |
| Multi-kind homepage first-class | Secondary until inventory solid |

## Competitive position

Win on **fresher, ranked, region-aware shortlists + digests + free proof**. Lose if competing on Easy Apply or free unlimited volume without ranking.

## Go / architecture notes

- Matching HTTP is intentional REST behind the gateway (not Connect) — consistent with this repo's candidate SPA contract.
- Gap-fill / store use `database/sql` + interfaces already established in `pkg/matching` (not greenfield Frame BaseRepository rewrite).
- New logic: weekly remaining via `CountNonOverflowThisWeek`, reason codes on `GapFillResult`, util.Log for non-fatal event writes.

## Verdict

```
PRODUCTION READINESS VERIFICATION
════════════════════════════════════════════════════
Requirements:     PASS (core seeker journey ships)
Assumptions:      FAIL → AUTH_REQUIRE_JWT + Path A + notify prod config
Scalability:      PASS WITH CONDITIONS (caps protect free tier)
Failure Modes:    PASS WITH CONDITIONS (empty reasons surface)
Data Integrity:   PASS (idempotent upsert; weekly remaining)
Security:         FAIL IF AUTH_REQUIRE_JWT unset in prod
Operations:       PASS WITH CONDITIONS (digest templates)

Critical Issues:  0 code-critical; 2 ops-critical (JWT, notify)

VERDICT:          READY WITH CONDITIONS

Blocking for paid ads scale:
  1. AUTH_REQUIRE_JWT=true + OIDC live
  2. Path A fan-out consumers registered
  3. Digest templates + notification Send metrics green
════════════════════════════════════════════════════
```
