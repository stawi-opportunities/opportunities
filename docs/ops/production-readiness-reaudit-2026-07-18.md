# Production readiness & market fit re-audit

**Date:** 2026-07-18  
**Head at audit start:** v8.0.196 (`a9e9409`) + follow-up honesty/cap fixes  
**Method:** Adversarial code review (matching, billing, UI, crawl, ops) against 10-section production checklist + seeker GTM funnel.

---

## Verdict

```
PRODUCTION READINESS VERIFICATION
════════════════════════════════════════════════════
Requirements:     PASS WITH GAPS (seeker core ships; monetization incomplete)
Assumptions:      FAIL (Path A queues, OIDC, notify, migrate-first, rebill)
Scalability:      PASS WITH CONDITIONS (HNSW + caps; Path A ops-fragile)
Failure Modes:    FAIL PARTIAL (silent notify, non-fatal fan-out publish, checkout ID split)
Data Integrity:   FAIL PARTIAL (free plan_id caps; uncapped preference persist; TOCTOU caps)
Security:         PASS WITH CONDITIONS (JWT default true; AUTH_REQUIRE_JWT=false is nuclear)
Operations:       FAIL PARTIAL (no in-repo env contract; notify/templates ops-critical)
Market honesty:   WAS FAIL → PARTIALLY FIXED this pass (pricing vapor, free paywall copy)

Critical issues:  4 residual (billing rebill, checkout identity, Path A wiring, preference uncapped)
High issues:      several (see below)

VERDICT:          READY WITH CONDITIONS for Starter GTM
                  NOT READY for scaled paid ads / Managed $200 narrative without billing + ops green

Blocking for scale:
  1. OIDC live (JWT fail-closed boots)
  2. Path A both queues + migration 0022 applied
  3. NOTIFICATION_SERVICE_URI + templates + Send metrics
  4. Billing: checkout ID consistency + recurring or expire period_end
  5. Cap all match writers (preference/HTTP persist) — still open
════════════════════════════════════════════════════
```

---

## 1. Requirements checklist (seeker product)

| # | Requirement | Status | Evidence |
|---|-------------|--------|----------|
| 1 | Browse jobs without signup | **IMPLEMENTED** | Public search/jobs; FAQ |
| 2 | Login required to apply (UI) | **IMPLEMENTED** | `OpportunityDetail.tsx` Sign in to apply |
| 3 | Login required to apply (API) | **PARTIAL** | Public `apply_url` still in jobs API (UI-only gate) |
| 4 | Free proof matches | **IMPLEMENTED** | Caps 1/3; refresh free; Path C now free-sub aware |
| 5 | Free tools (CV + job-fit) | **IMPLEMENTED** | `/me/tools/*`, ToolsPanel |
| 6 | Dismiss matches | **IMPLEMENTED** | `match_id` feed + dismiss route |
| 7 | Conversation-grounded Path A | **IMPLEMENTED** | Persona v1 + fan-out + dual-writer |
| 8 | Honest pricing $10/$200 | **IMPLEMENTED** | Catalog + cards; pricing table fixed this pass |
| 9 | No auto-apply / agent vapor | **PARTIAL** | Catalog false; activation auto_apply fixed; Hugo table fixed; residual i18n possible |
| 10 | Digests | **PARTIAL** | Code paths exist; ops templates required |
| 11 | Monthly recurring charge | **MISSING** | One-shot activate; no Collection rebill wired |
| 12 | Free → paid conversion UX | **PARTIAL** | Free banner OK; header/billing copy fixed this pass |

---

## 2. Implicit assumptions (risks)

| Assumption | Risk if violated |
|------------|------------------|
| Worker `MATCHING_FANOUT_QUEUE_URL` == matching `OPPORTUNITY_FANOUT_QUEUE_URI` | Path A dead; only refresh/digest |
| OIDC configured (no `AUTH_REQUIRE_JWT=false`) | Service won't start OR full header spoof |
| Migration `0022` before new matching pods | SQL errors on `rerank_text` / `conversation_digest` |
| `NOTIFICATION_SERVICE_URI` + templates live | Matches collect; digests/alerts silent fail |
| Checkout ledger key == SPA poll id == webhook id | Paid-but-free stuck until reconcile |
| Reconcile Trustage + ADMIN_SHARED_SECRET | No recovery if webhook misses |
| JWT `sub` == `candidate_profiles.id` | Empty or wrong tenant data |

---

## 3. Scalability

| Resource | Bottleneck | Failure mode | Mitigation present |
|----------|------------|--------------|--------------------|
| Path A KNN | HNSW top 500 | Misses beyond top-K | Caps + min_score |
| Chat re-embed | Every chat turn can embed | Cost spike | Rerank-text skip; **debounce still TODO** |
| Fan-out concurrency | `max_ack_pending` × replicas | Cap TOCTOU overshoot | Soft overflow status |
| Notification | External service | Silent skip | Log warn only |

---

## 4. Failure modes

| Mode | Behavior | Recovery |
|------|----------|----------|
| Path A publish fail after embed | Non-fatal | Path C / refresh / digest |
| Notify Send error | Ignored `_ = Send` | Manual ops |
| Checkout store nil | Redirect still returned | **Bad** — activation impossible |
| Dual-writer race | Persona wins after first chat | First CV-only index until persona |
| Path A nested under Path C flag | Disabling C kills A | Code coupling |

---

## 5. Data integrity / entitlements

| Issue | Severity | Status after this pass |
|-------|----------|------------------------|
| Free users with onboard `plan_id=starter` got paid caps on Path A/C | **Critical** | **FIXED** — `planEntitlements` uses subscription first |
| Path C missing weekly remaining | **Critical** | **FIXED** — `WeekCount: Store` wired |
| Preference/HTTP `PersistMatchResult` uncapped | **Critical** | **OPEN** |
| Free Managed upgrade via change-plan | **Critical** | **FIXED** — checkout_required for starter→managed |
| ActivateSubscription set `auto_apply=true` for managed | **High** | **FIXED** — always false |
| Daily cap double-count / race | Medium | OPEN |

---

## 6. Security

| Control | Status |
|---------|--------|
| JWT required by default | **PASS** |
| Fail closed without OIDC | **PASS** |
| Public endpoints opt-out only | **PASS** (healthz, plans, webhook) |
| `AUTH_REQUIRE_JWT=false` | Nuclear; ops only |
| Admin secret fail-closed | **PASS** if unset |
| Public apply_url | Intentional open API; not identity risk |

---

## 7. Market fit scorecard

| Stage | Grade | Notes |
|-------|-------|-------|
| Land | B- | Jobs-first improved; multi-kind “coming soon” still dilutes |
| Browse free | A- | Real inventory |
| Login-to-apply | A- | UI solid; API still exposes URL |
| Free proof | A- | Caps + tools + dismiss; persona matching |
| Honesty | B+ | Pricing vapor **fixed this pass**; residual homepage claims |
| Pay Starter $10 | B | Real value if digests+Path A live |
| Pay Managed $200 | C | Real delta is uncapped volume; no agent; rebill missing |
| Digests | Ops | Value invisible if notify down |

**Why it makes sense to use (if ops green):**  
Browse free → login to apply → free shortlist from CV+chat persona → free tools → pay for more volume/digests.

**Why it fails market fit if ops/billing lag:**  
Paid ads promise monthly Managed with digests while Path A is dark, notify silent, and billing may not rebill or activate reliably.

---

## 8. Critical residual issues (do not greenwash)

### C1 — Billing: checkout identity split (order_ref vs session ref)
Ledger/prompt_id may not match SPA return URL → activation races.  
**Fix:** single ID across create, return URL, poll, webhook, reconcile.

### C2 — Billing: no recurring rebill
Monthly pricing without Collection lifecycle or period_end expiry = perpetual free ride after one payment.  
**Fix:** wire rebill **or** demote to free when period ends without payment.

### C3 — Path A ops wiring
Both worker publish URL and matching subscribe URI must be set; defaults do not join multi-service.  
**Fix:** deploy checklist + alert if either side missing under load.

### C4 — Uncapped preference / HTTP match persist
`PersistMatchResult` can write many `status=new` without daily/weekly.  
**Fix:** route through GapFill/caps or enforce before insert.

### C5 — Migration 0022 before rollout
`rerank_text` / `conversation_digest` required by new SQL.  
**Fix:** migrate-first deploy gate.

---

## 9. Fixed in this re-audit pass

1. CI: unused `composeSummary` (golangci unused)  
2. Pricing page: remove auto-apply / interview prep / agent 1:1 vapor  
3. Dashboard header: free proof copy (not “finish payment to unlock”)  
4. View plans for unpaid → `#billing` (not dead PlanChangeModal)  
5. CompletePaymentPanel: honest free-vs-paid copy  
6. Free-proof caps on Path C for non-paid subscription  
7. Path C weekly remaining  
8. Block free Starter→Managed plan change without checkout  
9. `auto_apply` always false on activate  

---

## 10. Minimum changes before scaled acquisition

| Priority | Item |
|----------|------|
| P0 | Staging dry-run: checkout → ledger → webhook → paid + caps |
| P0 | Path A LIVE logs both services + one end-to-end new job match |
| P0 | Digest email to a real paid user |
| P0 | Apply C4 (cap preference persist) |
| P1 | Checkout ID unification |
| P1 | Recurring or period expiry |
| P1 | Homepage multi-kind stats honesty |
| P2 | Debounce persona re-embed; Path A/C flag decoupling |

---

## Bottom line

The **seeker product spine is real and largely market-fit** for a honest Starter GTM story: free browse, login-to-apply, free proof matches/tools, conversation-grounded Path A design, JWT fail-closed.

**Do not claim production-ready SaaS monetization** until billing identity + rebill (or expiry), Path A dual-queue LIVE, notify green, and remaining uncapped match writers are closed.

**Managed $200** is only defensible as uncapped discovery + priority alerts — never agent/auto-apply.
