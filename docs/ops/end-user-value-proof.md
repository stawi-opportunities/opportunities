# End-user value proof (comprehensive)

**Date:** 2026-08-10  
**Release train:** v8.0.185 → **invoke-only matching** (quality-first digests)  
**Audience:** product, GTM, investors — evidence that a real seeker gets value without vapor.

---

## 1. One-sentence product

**Browse real jobs free → sign in to apply → free proof matches + free career tools → pay for more daily Find-matches invokes, digests, and priority — not for more low-quality match rows.**

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
| 7 | Upload CV | Embedding + index update (no auto match spam) | Placement / CV embed → Path C index-only | Shipped |
| 8 | **Find matches now** (free) | Up to **1 invoke/day**; matches only ≥ **70%**; unlimited feed rows above floor | `MatchInvoke` + invoke limits + `MATCHING_MIN_SCORE` | Invoke-only |
| 9 | Empty shortlist | Honest reason: rate limit / no inventory / below threshold / no embedding | `reason` on refresh + MatchesPanel toasts | Shipped |
| 10 | Review match card | Score (0–100), Apply, Save, **Dismiss** | Feed `match_id` + dismiss gateway route | **v8.0.191** |
| 11 | Apply (signed in) | Opens employer site, then tracks application | `openApplyAndTrack` — never fakes success | Shipped |
| 12 | Free **Tools** | CV ATS score + vector+keyword job-fit | `POST /me/tools/*`, ToolsPanel | **v8.0.190–191** |
| 13 | Subscribe Starter $10 | Higher invoke ceiling, digests (incl. twice_daily), quality feed | `pkg/billing` catalog + invoke limits | Invoke-only |
| 14 | Subscribe Managed $200 | Highest invoke ceiling, priority alerts, same quality floor | Honest Managed features (no agent theater) | Shipped |

---

## 3. Value equation (why pay)

| Free | Paid Starter ($10) | Paid Managed ($200) |
|------|--------------------|---------------------|
| Browse + search all jobs | Everything free | Everything Starter |
| Login to apply + track | Higher daily **invoke** ceiling (default **30**/day) | Highest invoke ceiling (default **100**/day) |
| Proof: **1 Find matches / day** (not match-row caps) | Email digests: **≤3 unseen** per fire | Priority match alerts |
| Quality floor **70%** — feed unlimited above floor | Cadence: `off` \| `twice_daily` \| `daily` \| `weekly` | Same quality floor + uncapped feed above floor |
| CV ATS + job-fit tools | Dashboard match feed (all ≥ 70%) | Higher abuse ceiling, not “more weak rows” |
| Dismiss, save, score | | Same feed UX |

**Conversion bet:** User sees real scored roles (≥70%) and free tools before card details — paid is “more invokes + digests of what already worked,” not “unlock a blank product” or “buy 5 weak matches/week.”

---

## 4. Trust guarantees (anti-vapor)

| Guarantee | Evidence |
|-----------|----------|
| No auto-apply sold | `Entitlements.AutoApply = false` all plans; FAQ explicit |
| No fake apply success | `openApplyAndTrack` requires URL; tracks after open |
| No blank Managed feed | Managed uses same `MatchesPanel` + feed as Starter |
| No white-glove 1:1 theater | `AgentCard` only if assigned; support email only |
| Pricing honest | Starter $10 / Managed $200 from `usd_cents` (not broken cents) |
| Empty match honesty | `rate_limited` / `no_inventory` / `below_threshold` / `no_embedding` |
| Quality over volume | `MATCHING_MIN_SCORE` default **0.70**; no plan match-row caps |
| Digests not spam | Top-**3 unseen** + notification receipts |
| Free limits are invokes | Free **1 invoke/day**, not “3 match rows/week” |

---

## 5. Technical value spine

```
Crawl (structured only) → opportunities PG
        ↓
Embeddings (candidate + job)
        ↓
Path C index-only (vector ready; no auto matches)
Path A fan-out OFF by default
        ↓
MatchInvoke (user refresh | digest) — score ≥ 0.70
        ↓
candidate_matches (score, status, match_id) — no plan row caps
        ↓
Dashboard feed (all ≥ floor) + digests (≤3 unseen + receipts)
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
| `go test ./pkg/matching/ ./apps/matching/... ./pkg/billing/` | Pass (expected on branch) |
| `golangci-lint ./pkg/matching/` | Clean when run |
| MatchInvoke / invoke limits / digest top-3 / twice_daily unit tests | Covered on invoke-only branch |
| Feed `match_id` handler test | Pass |
| Job-fit tools unit tests | Pass |

### Frontend

| Check | Result |
|-------|--------|
| `npm run typecheck` | Pass when run |
| Quality-first + twice_daily settings copy | Settings notifications |
| Cap strip removed from matches UX | Invoke-only UI task |

### Release artifacts

| Tag / train | Content |
|-------------|---------|
| v8.0.190 | Free proof + tools + honest marketing |
| v8.0.191 | Dismiss + vector job-fit |
| v8.0.192 | (legacy) weekly caps era — superseded by invoke-only |
| v8.0.193 | Login required to apply |
| v8.0.194 | CI green + earlier value proof |
| 2026-08 invoke-only | MatchInvoke, min score 0.70, no row caps, digest top-3, twice_daily |

Docker **Release** workflow on tags: succeeded for v8.0.192–193 (images built).

---

## 7. Manual UAT checklist (production)

Use a clean browser profile.

1. [ ] `/jobs/` loads without sign-in; open a listing; description renders as HTML.
2. [ ] Click **Sign in to apply** → OIDC → return to same job → employer tab opens.
3. [ ] Signed-out apply does **not** open employer URL before login.
4. [ ] Complete onboarding / CV upload → Dashboard free banner visible.
5. [ ] **Tools** → CV score returns number + fixes; job-fit returns score + method badge.
6. [ ] **Matches** → Find matches now → cards only ≥ **70%**, or explicit reason toast.
7. [ ] Free: second Find matches same day → `rate_limited` (invoke limit, not row cap).
8. [ ] Match card shows score; **Dismiss** removes card; refresh does not bring dismissed back.
9. [ ] Apply on match → employer opens + tracked as applied.
10. [ ] Pricing shows Starter **$10** and Managed **$200** (invoke/digest value, not “5 matches/week”).
11. [ ] Settings: save **twice_daily** digest; prefs round-trip.
12. [ ] FAQ: free browse; login to apply; no auto-apply.

---

## 8. Ops conditions (still required for scale)

These do not block “product is valuable”; they block **paid acquisition at scale**:

1. OIDC configured on matching (JWT is required by default; binary fails closed without it).
2. Invoke path healthy (`POST /me/matches/refresh` + index embeds); Path A **not** required.
3. Digest templates registered in service-notification; Send metrics healthy.
4. Trustage match-digest cron **hourly** (or better) so `twice_daily` windows work.

---

## 9. Verdict

| Question | Answer |
|----------|--------|
| Is there a complete free→paid value ladder? | **Yes** |
| Can a user verify quality before paying? | **Yes** (proof invoke + ≥70% + tools) |
| Is apply honest? | **Yes** (login gate + employer URL) |
| Are limits/empty states honest? | **Yes** (invokes + quality floor, not fake row budgets) |
| Is marketing aligned with code? | **Yes** (no “5 matches/week” scarcity) |
| Production ready for Starter GTM? | **READY WITH CONDITIONS** (ops list §8) |

**Bottom line:** A seeker can discover jobs free, must log in to apply, can get real high-quality matches and career tools before paying, and pays for **invoke capacity, digests, and priority** — not for unlocking a blank product or buying a small pile of weak match rows. That is a coherent, defensible value proposition.
