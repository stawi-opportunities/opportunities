# Invoke-only matching & quality-first digests

**Date:** 2026-08-10  
**Status:** Approved  
**Branch target:** `main` via feature branch  
**Scope:** Track A only (seeker matching economics). Employer ATS is Track B and out of scope.

## Goal

Reduce matching infrastructure load and raise match quality by:

1. Running match **generation only when invoked** (user refresh or scheduled digest), not on every new job or CV embed.
2. Applying a **tight quality floor (70%)** instead of counting how many matches a plan may store.
3. Keeping the **in-app feed uncapped by count** (all matches ≥ 70%).
4. Constraining **outbound** email/WhatsApp to **at most 3 highest-scoring unseen** matches per digest.
5. Supporting digest cadence: **off | twice_daily | daily | weekly** for paid subscribers.

Accurate matching is the product differentiator for the two paid plans (Starter / Managed). Auto-apply and employer ATS are deferred.

## Background (current system)

| Path | Today | Problem |
|------|--------|---------|
| Path A fan-out | New opportunity embed → reverse-KNN → `candidate_matches` | Cost scales with job ingest × candidate index |
| Path C gap-fill | Persona/CV embed → matches | Background matching without user intent |
| Caps | Free 1/day·3/week; Starter ~5/week; Managed “unlimited” via high caps | Monetizes **volume** of rows, not quality; overflow complexity |
| Score floor | `MATCHING_MIN_SCORE` default ~0.45 | Too many weak matches |
| Digests | daily / weekly / off | No twice-daily; digest content not strictly “top 3 unseen” |

Applications (`pkg/applications`) remain **seeker-side** tracking only. This design does not add employer ATS.

## Product rules (decisions)

| Decision | Choice |
|----------|--------|
| When matching runs | **User invoke + digest only** |
| Approach | **Explicit `MatchInvoke` entry point** (Approach B) |
| In-app match count | **Unlimited** above quality floor |
| Quality floor | **0.70** (70% display), all tiers |
| Free users | Same quality filter; **limited invokes** (1/day default) |
| Outbound digest size | **Top 3 unseen** above threshold |
| Paid digest cadences | `off` \| `twice_daily` \| `daily` \| `weekly` |
| Plan differentiation | Invoke rate ceilings + digest cadence; **not** match-row caps |
| Path A | Default **off** (kill-switch may remain) |
| Path C | **Index-only** (no automatic GapFill matches) |

## Architecture

```
[User: Find matches] ──► POST /me/matches/refresh ──► MatchInvoke(user_refresh)
                                                         │
[Trustage cron] ──► admin digest ──► MatchInvoke(digest)─┤
                                                         ▼
                                              score ≥ 0.70 upsert
                                                         │
                    ┌────────────────────────────────────┼────────────────────────┐
                    ▼                                    ▼                        ▼
               In-app feed                        Digest picker              No Path A
            (all ≥ 0.70)                   (top 3 unseen → notify)         fan-out
```

**Index-only events** (do not call `MatchInvoke`):

- Opportunity embedding complete (former Path A trigger)
- CV / persona embedding complete (former Path C gap-fill)
- Preference / profile field updates (vector may update; matches wait for next invoke)

Optional **one-shot** `MatchInvoke(onboard_seed)` after first successful index may remain for proof UX; it must respect free invoke budget or be explicitly documented as the free daily invoke.

## Component design

### 1. `MatchInvoke(candidateID, reason)`

Single shared implementation used by HTTP refresh and digest jobs.

**Reasons:**

| Reason | Source | Consumes user invoke budget? | Sends notification? |
|--------|--------|------------------------------|---------------------|
| `user_refresh` | `POST /me/matches/refresh` | Yes | No (in-app only) |
| `digest` | Admin/Trustage digest | No | Yes, if ≥1 unseen eligible match after pick |
| `onboard_seed` | Post-index seed (optional) | Yes (or counts as free daily) | No |

**Steps:**

1. Load candidate, subscription, entitlements, match index embedding.
2. If no usable vector → attempt rebuild if policy allows; else return `no_embedding`.
3. If reason is user-facing and invoke rate exceeded → return `rate_limited` (no KNN).
4. Reverse-KNN + existing score blend (cosine 0.60, skills 0.15, geo 0.15, salary 0.10, stale −0.10).
5. Drop results with blended score **&lt; 0.70**.
6. Upsert into `candidate_matches` (score-monotonic; preserve dismissed/applied/terminal states).
7. **Do not** apply daily/weekly match-row caps; **do not** write overflow rows for plan limits.
8. Return summary: written count, top scores, `reason` code.

### 2. Invoke rate limits (replace match-count caps)

| Tier | Default invokes / calendar day | Role |
|------|--------------------------------|------|
| Free (unpaid) | 1 | Proof without open-ended KNN |
| Starter | 30 | Abuse ceiling, not scarcity pricing |
| Managed | 100 | Higher abuse ceiling |

Env overrides:

- `MATCHING_INVOKE_LIMIT_FREE` (default `1`)
- `MATCHING_INVOKE_LIMIT_STARTER` (default `30`)
- `MATCHING_INVOKE_LIMIT_MANAGED` (default `100`)

Implementation notes:

- Count successful or attempted `user_refresh` / `onboard_seed` invocations per candidate per day (timezone: UTC or profile timezone—pick **UTC** for simplicity unless profile timezone already drives digests; document choice in code).
- Digest invocations never decrement the user budget.
- Entitlements API should expose `invoke_daily_limit` (and remaining if cheap to compute). Stop presenting weekly match caps as product truth.

### 3. Quality floor & storage

- Default `MATCHING_MIN_SCORE=0.70`.
- Sync `candidate_match_indexes.min_score` to 0.70 on index writes.
- Feed queries: active (non-dismissed) matches with score ≥ min score; **no** overflow filter required for plan honesty (ignore legacy overflow rows).
- Migration/cleanup: optional SQL to hide or delete overflow rows; not blocking if queries ignore them.
- Empty refresh reasons:

| Code | Meaning |
|------|---------|
| `ok` | Invoke completed; zero or more matches ≥ 0.70 present |
| `rate_limited` | User invoke budget exhausted |
| `no_embedding` | Cannot match yet |
| `below_threshold` | KNN ran; nothing ≥ 0.70 |
| `no_inventory` | No searchable active opportunities |

Remove paid-user empty states driven by `weekly_cap` / `daily_cap` match budgets.

### 4. Path A / Path C product defaults

| Flag / behavior | Default | Notes |
|-----------------|---------|--------|
| `MATCHING_FANOUT_ENABLED` | `false` | Path A consumer off; code may remain as emergency kill-switch |
| Candidate-change consumer | On for **index update only** | Do not call GapFill / match insert |
| Preference rematch consumer | Off or index-only | No automatic match generation |

Worker may still publish fan-out messages if configured; matching service must not process them when fan-out disabled. Prefer not publishing when disabled to save queue noise (`MATCHING_FANOUT_QUEUE_URL` unset or explicit gate).

### 5. Digests & notifications

**Channel split:**

| Surface | Content |
|---------|---------|
| In-app | All matches ≥ 0.70 (uncapped count) |
| Email / WhatsApp digest | Up to **3** highest-scoring **unseen** matches |
| Real-time `match_alerts` | Out of scope for v1 of this change (remain opt-in legacy if present; do not expand) |

**Unseen (outbound):**

A match is unseen if it has no successful outbound receipt for this candidate.

Persist receipts, preferred shape:

```text
match_notification_receipts
  candidate_id, match_id, channel, sent_at
  unique (candidate_id, match_id, channel)
```

Alternatively `last_notified_at` on `candidate_matches` if a full receipts table is deferred—but receipts are preferred so channels stay independent.

**Digest pipeline:**

1. Select entitled candidates whose cadence matches this run (`ShouldSendDigest` extended).
2. `MatchInvoke(reason=digest)` (freshness; no user budget).
3. Query matches: score ≥ 0.70, not dismissed, no receipt, order by score desc, **limit 3**.
4. If empty → skip notification.
5. `NotificationService.Send` with digest template; write receipts.

**Cadence values** (`candidate_profiles.email_digest`):

| Value | Behavior |
|-------|----------|
| `off` | Never |
| `daily` | Once per local day in configured send window |
| `twice_daily` | Two local windows (e.g. 08:00–10:00 and 17:00–19:00) |
| `weekly` | Once per week on `DIGEST_WEEKLY_WEEKDAY` |

- Extend `NormalizeDigestCadence` and `ShouldSendDigest` for `twice_daily`.
- Trustage: keep or add cron ticks that hit digest admin endpoint often enough for twice-daily windows; server-side filter remains source of truth.
- Free/unpaid: no paid match digests (existing unpaid re-engagement jobs, if any, stay separate).

**Templates:** payload `matches` length 0–3; copy must not imply uncapped email volume.

### 6. Plans & copy

| Capability | Free | Starter | Managed |
|------------|------|---------|---------|
| Browse/search | Yes | Yes | Yes |
| Min score | 0.70 | 0.70 | 0.70 |
| In-app match rows | Unlimited ≥ floor | Unlimited | Unlimited |
| Find matches / day | 1 | ~30 | ~100 |
| Match digests | No | off/twice_daily/daily/weekly | Same |
| Digest highlights | — | ≤3 unseen | ≤3 unseen |
| Auto-apply | false | false | false |
| Priority hint | proof | standard | agent |

Update:

- `pkg/billing` catalog descriptions
- `ui/app` `plans.ts` and Matches empty/budget UI
- Ops docs: `matching-pipeline.md`, `end-user-value-proof.md` (remove “5 matches/week” style claims)

**Honest monetization:** subscription buys **ongoing discovery cadence** (invokes + digests), not a larger pile of weak matches.

### 7. API & UI

**API**

- `POST /me/matches/refresh` → rate limit → `MatchInvoke(user_refresh)` → body includes `reason`, matches, invoke remaining if available.
- Digest admin endpoints continue to exist; filter by extended cadence.
- Settings/notifications PATCH accepts `email_digest=twice_daily`.
- Entitlements JSON: `invoke_daily_limit`; deprecate product use of match `daily_cap`/`weekly_cap` for feed truncation (compat fields may remain zero or unused).

**UI (mobile-first)**

- Matches: remove weekly budget strip for paid; free shows remaining free invoke(s).
- Empty states: rate limited / below threshold / no inventory / finish CV.
- Settings → notifications: cadence includes **Twice daily**.
- No employer surfaces in this track.

## Error handling & reliability

- Nil notification client: digest skips send, logs, does not fail match upsert.
- Invoke timeout / KNN failure: return 5xx or structured error; do not partial-write inconsistent caps.
- Score-monotonic upsert and terminal status preservation remain mandatory.
- Fan-out disabled must not block opportunity embed success path.

## Testing

| Area | Cases |
|------|--------|
| Invoke-only | Fan-out / candidate-change do not insert matches when product defaults on |
| Threshold | 0.69 rejected; 0.70 accepted; many rows allowed for one user |
| Rate limit | Free second `user_refresh` same day → `rate_limited` |
| Digest pick | ≤3 unseen; second digest does not resend same match_ids |
| Cadence | Matrix for off/daily/twice_daily/weekly × run time |
| Entitlements | Free vs Starter vs Managed invoke limits |
| UI | Cap strip removed; twice_daily option; free invoke copy |
| Regression | Dismissed/applied not overwritten; blend weights unchanged |

## Rollout

1. Deploy matching with fan-out default off, min score 0.70, `MatchInvoke`, invoke limits, digest top-3 + receipts, twice_daily.
2. Deploy UI copy/settings.
3. Confirm Trustage cron frequency supports twice_daily windows.
4. Monitor: invoke latency, digests sent/day, empty-feed rate, KNN QPS (should drop vs Path A).
5. If sparse markets yield empty feeds, consider **floor tuning** later—not reintroducing count caps.

## Non-goals

- Employer ATS, company profiles, job posting, embed widgets, interview scheduling (Track B).
- Changing score blend weights (floor only).
- Auto-apply product.
- Expanding real-time per-match WhatsApp/email.
- Guaranteeing non-empty feeds in all markets.

## Key Decisions

| Decision | Rationale |
|----------|-----------|
| Explicit `MatchInvoke` only | Prevents accidental infra burn; matches product language |
| 70% global floor | Quality over volume; same accuracy for all tiers |
| No in-app count caps | Users enjoy all strong matches; monetize cadence not row count |
| Outbound max 3 unseen | Avoid spam on email/WhatsApp while feed stays rich |
| Free = 1 invoke/day | Proof without open matching |
| Path A off / Path C index-only | Largest load reduction |
| twice_daily cadence | “Higher frequency” without real-time spam |
| ATS deferred | Separate product; seeker value ships first |

## Open Questions

None blocking. Implementation may choose UTC vs profile timezone for invoke day boundaries; default **UTC** unless existing rate-limit code already uses another zone—match existing patterns.

## PR Plan

### PR1 — MatchInvoke core + threshold + disable auto paths
- **Affects:** `apps/matching`, `pkg/matching`, config defaults, fan-out/candidate-change wiring
- **Deps:** none
- **Changes:** Introduce `MatchInvoke`; raise default min score to 0.70; fan-out default off; Path C index-only; stop plan overflow writes; refresh handler uses invoke + rate limit

### PR2 — Entitlements & invoke limits
- **Affects:** `pkg/billing`, placement/index sync, refresh API response, tests
- **Deps:** PR1
- **Changes:** Map plans to invoke limits; stop using DailyCap/WeeklyCap for match-row truncation; free 1/day

### PR3 — Digest top-3 unseen + twice_daily
- **Affects:** `pkg/matching` digest schedule, digest admin handlers, receipts persistence/migration, Trustage defs, notify payloads
- **Deps:** PR1
- **Changes:** Receipts; pick ≤3 unseen; extend cadence; cron notes for twice_daily

### PR4 — UI & product copy
- **Affects:** `ui/app` Matches, Settings notifications, `plans.ts`, empty states
- **Deps:** PR2, PR3 (can soft-land after PR2 with feature-detect)
- **Changes:** Remove budget strip; free invoke copy; twice_daily option; plan marketing text

### PR5 — Docs & ops alignment
- **Affects:** `docs/ops/matching-pipeline.md`, `end-user-value-proof.md`, Trustage README
- **Deps:** PR1–PR4
- **Changes:** Document invoke-only model, env table, cadence, remove cap-centric claims

Each PR should be independently reviewable and include unit tests for its surface.
