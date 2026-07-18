# End-user value proof (comprehensive)

**Date:** 2026-07-18  
**Release train:** v8.0.185 → **v8.0.194** (this proof)  
**Audience:** product, GTM, investors — evidence that a real seeker gets value without vapor.

---

## 1. One-sentence product

**Browse real jobs free → sign in to apply → free proof matches + free career tools → pay only for more weekly matches and digests.**

---

## 2. Journey map with shipped proof

| # | User moment | What they get | Code / surface proof | Status |
|---|-------------|---------------|----------------------|--------|
| 1 | Land on homepage | Jobs-first promise; free first matches; no auto-apply claims | `ui/layouts/index.html`, FAQ honesty | Shipped |
| 2 | Search / browse jobs | Full listing inventory without account | Public API search + `JobRow` → detail only | Shipped |
| 3 | Open a job | HTML description, company, location, newest listings | Snapshot + `DescriptionBody` HTML | Shipped |
| 4 | Click **Apply** (signed out) | **Must sign in** — no silent external apply | `OpportunityDetail` ApplyLink → login + `?apply=1` | **v8.0.193** |
| 5 | Complete login | Return to same job; employer apply URL opens once | `resolvePostLoginPath` content restore + auto-apply | **v8.0.193** |
| 6 | Sign in / onboard | Dashboard without hard paywall; free proof CTA | `Dashboard.tsx` free banner; onboarding free matches CTA | **v8.0.190** |
| 7 | Upload CV | Embedding pipeline for matching | Placement / CV embed queues | Shipped |
| 8 | **Find matches now** (free) | Up to 1/day, 3/week quality matches (remaining budget) | Gap-fill + free entitlements + weekly remaining | **v8.0.190–192** |
| 9 | Empty shortlist | Honest reason: weekly cap / daily / no inventory / threshold | `reason` on refresh + MatchesPanel toasts | **v8.0.192** |
| 10 | Review match card | Score (0–100), Apply, Save, **Dismiss** | Feed `match_id` + dismiss gateway route | **v8.0.191** |
| 11 | Apply (signed in) | Opens employer site, then tracks application | `openApplyAndTrack` — never fakes success | Shipped |
| 12 | Free **Tools** | CV ATS score + vector+keyword job-fit | `POST /me/tools/*`, ToolsPanel | **v8.0.190–191** |
| 13 | Subscribe Starter $10 | 5 matches/week, digests, match feed | `pkg/billing` catalog + `plans.ts` | Shipped |
| 14 | Subscribe Managed $200 | Unlimited discovery, priority alerts, same feed uncapped | Honest Managed features (no agent theater) | Shipped |

---

## 3. Value equation (why pay)

| Free | Paid Starter ($10) | Paid Managed ($200) |
|------|--------------------|---------------------|
| Browse + search all jobs | Everything free | Everything Starter |
| Login to apply + track | Higher weekly cap (5) | Unlimited weekly |
| Proof matches (1/day, 3/week) | Email digests | Priority match alerts |
| CV ATS + job-fit tools | Dashboard match feed | Uncapped refresh budget |
| Dismiss, save, score | | Faster gap-fill priority |

**Conversion bet:** User sees 1–3 real scored roles and free tools before card details — paid is “more of what already worked,” not “unlock the product.”

---

## 4. Trust guarantees (anti-vapor)

| Guarantee | Evidence |
|-----------|----------|
| No auto-apply sold | `Entitlements.AutoApply = false` all plans; FAQ explicit |
| No fake apply success | `openApplyAndTrack` requires URL; tracks after open |
| No blank Managed feed | Managed uses same `MatchesPanel` + feed as Starter |
| No white-glove 1:1 theater | `AgentCard` only if assigned; support email only |
| Pricing honest | Starter $10 / Managed $200 from `usd_cents` (not broken cents) |
| Empty match honesty | `weekly_cap` / `daily_cap` / `no_inventory` / `below_threshold` |
| Weekly caps real | `CountNonOverflowThisWeek` remaining budget, not per-run slice |

---

## 5. Technical value spine

```
Crawl (structured only) → opportunities PG
        ↓
Embeddings (candidate + job)
        ↓
Path A fan-out (new jobs) + Path C gap-fill (Find matches now)
        ↓
candidate_matches (score, status, match_id)
        ↓
Dashboard feed + dismiss + digests (service-notification)
        ↓
Apply → employer URL + applications row
```

Free tools branch:

```
CV text / profile → CV ATS scorer
Profile embedding × job embedding (stored or live) + keywords → job-fit score
```

---

## 6. Automated verification (this release)

### Backend

| Suite | Result |
|-------|--------|
| `go test ./pkg/matching/ ./apps/matching/... ./pkg/billing/` | Pass |
| `golangci-lint ./pkg/matching/` | 0 issues (ineffassign fixed) |
| Gap-fill weekly cap + no-inventory reasons unit tests | Pass |
| Feed `match_id` handler test | Pass |
| Job-fit tools unit tests | Pass |

### Frontend

| Check | Result |
|-------|--------|
| `npm run typecheck` | Pass |
| `npm run lint` | Pass |
| `prettier --check` | Pass |
| postLoginRedirect (login-to-apply paths) | Pass |
| HomeRedirect + OpportunitySideChat | Pass |

### Release artifacts

| Tag | Content |
|-----|---------|
| v8.0.190 | Free proof + tools + honest marketing |
| v8.0.191 | Dismiss + vector job-fit |
| v8.0.192 | Weekly caps + empty reasons + free overview value |
| v8.0.193 | Login required to apply |
| v8.0.194 | CI green (lint/prettier) + this value proof |

Docker **Release** workflow on tags: succeeded for v8.0.192–193 (images built).

---

## 7. Manual UAT checklist (production)

Use a clean browser profile.

1. [ ] `/jobs/` loads without sign-in; open a listing; description renders as HTML.
2. [ ] Click **Sign in to apply** → OIDC → return to same job → employer tab opens.
3. [ ] Signed-out apply does **not** open employer URL before login.
4. [ ] Complete onboarding / CV upload → Dashboard free banner visible.
5. [ ] **Tools** → CV score returns number + fixes; job-fit returns score + method badge.
6. [ ] **Matches** → Find matches now → either cards or explicit reason toast.
7. [ ] Match card shows score; **Dismiss** removes card; refresh does not bring dismissed back.
8. [ ] Apply on match → employer opens + tracked as applied.
9. [ ] Pricing shows Starter **$10** and Managed **$200**.
10. [ ] FAQ: free browse; login to apply; no auto-apply.

---

## 8. Ops conditions (still required for scale)

These do not block “product is valuable”; they block **paid acquisition at scale**:

1. `AUTH_REQUIRE_JWT=true` + OIDC in production matching.
2. Path A fan-out consumers + queues live.
3. Digest templates registered in service-notification; Send metrics healthy.

---

## 9. Verdict

| Question | Answer |
|----------|--------|
| Is there a complete free→paid value ladder? | **Yes** |
| Can a user verify quality before paying? | **Yes** (proof matches + tools) |
| Is apply honest? | **Yes** (login gate + employer URL) |
| Are caps/empty states honest? | **Yes** |
| Is marketing aligned with code? | **Yes** (vapor removed) |
| Production ready for Starter GTM? | **READY WITH CONDITIONS** (ops list §8) |

**Bottom line:** A seeker can discover jobs free, must log in to apply, can get real matches and career tools before paying, and pays for volume/digests/priority — not for unlocking a blank product. That is a coherent, defensible value proposition.
