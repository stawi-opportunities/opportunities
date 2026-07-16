# Extensible plan: automatic recurring subscriptions via service-billing

**Status:** implemented (core path)  
**Goal:** Make recurrent payments **trivial for product services** (Opportunities, future products) by owning schedule + invoices in **service-billing**, charging through **checkout/payment/Flutterwave v4 only**, and treating product DBs as an **entitlement cache** only.

### Implementation notes (live code)

| Piece | Location |
|-------|----------|
| COF collector (v4 flutterwave only) | `payment_collect.go` |
| **Per-sub Trustage scheduler** | `renewal_scheduler.go` + `renewal_plan.go` — one workflow per subscription |
| Process one sub | `POST /_internal/billing/subscriptions/{id}/renew` → `ProcessSubscription` |
| Dunning | retry CSV `0,24,72,168`; max attempts **archives** Trustage workflow |
| Soft cancel | Reschedule to period-end finalize; archive after hard cancel |
| Bulk renew | **Removed** — only `ProcessSubscription(id)` via Trustage one-shot |
| Instrument pin + first schedule | `ConfirmPayment` → pin COF + **Ensure** Trustage one-shot |
| Flutterwave | v4 OAuth only for token charges |
| Product | `pkg/billing` Collection + lifecycle mirror |

---

## 1. Principles

| Principle | Meaning |
|-----------|---------|
| **Billing is SoT** | Subscription state, periods, invoices, and “what is owed” live in service-billing. |
| **Payment is rails** | Checkout + payment + Flutterwave only move money; they do not own “monthly plan”. |
| **Product is entitlement** | Opportunities mirrors `ACTIVE` / `CANCELLED` into `candidate_profiles` for fast gates. |
| **One product API** | Products call a thin **Collection** surface, not PSP-specific APIs. |
| **Interactive once** | First payment may need browser/3DS; renewals are **server-side COF** when a token exists. |
| **Extensible collectors** | Hosted card, token charge, future MoMo wallet — same invoice + settle contract. |

---

## 2. Target architecture

```
┌──────────────────────────────────────────────────────────────────────────┐
│ Product (Opportunities matching / SPA)                                   │
│  • StartSubscription / Cancel / GetSubscription (via billing client)     │
│  • Entitlement cache: candidate_profiles.subscription = paid|cancelled   │
│  • Consume lifecycle queue: activated | billed | cancelled               │
└────────────────────────────┬─────────────────────────────────────────────┘
                             │ CollectionService + lifecycle events
                             ▼
┌──────────────────────────────────────────────────────────────────────────┐
│ service-billing                                                          │
│  • Catalog (plans/components FLAT monthly)                               │
│  • Subscription (ACTIVE/PENDING/CANCELLED, billing_anchor)               │
│  • Invoice (ISSUED → PAID / OVERDUE)                                     │
│  • RenewalScheduler: due ACTIVE subs → invoice → Collect                 │
│  • Collectors:                                                           │
│       HostedCheckout  (first pay / update card / 3DS fallback)           │
│       SavedInstrument (silent COF via payment InitiatePrompt)  [NEW]     │
│  • Settlement sweeper (already exists for hosted sessions)               │
└─────────────┬───────────────────────────────┬────────────────────────────┘
              │                               │
              ▼                               ▼
┌─────────────────────────┐     ┌─────────────────────────────────────────┐
│ service-payment-checkout│     │ service-payment + flutterwave           │
│  pay.stawi.org session  │     │  InitiatePrompt                         │
│  write profile clues:   │     │    • card extras (first)                │
│  payment_method_id,     │     │    • payment_method_id + customer_id    │
│  customer_id            │     │      + recurring=true (renew) [EXISTS]  │
└─────────────────────────┘     └─────────────────────────────────────────┘
```

**Flutterwave v4** does not own payment plans; billing owns the subscription, FW only executes charges (documented in billing/flutterwave integration).

---

## 3. Domain contracts (keep thin)

### 3.1 What products store (Opportunities)

| Field | Role |
|-------|------|
| `subscription_id` | Billing subscription ID (SoT pointer) |
| `plan_id` | Product tier key for UX/entitlements |
| `subscription` | `paid` \| `free` \| `cancelled` \| `past_due` (cache) |
| `current_period_end` | Mirror of billing period for UI |
| `cancel_at_period_end` | Mirror of billing cancel intent |

Optional later: drop local amount ledger; keep `candidate_checkouts` only as UX history or replace with invoice list from billing.

### 3.2 What billing stores

| Entity | Role |
|--------|------|
| `Subscription` | Profile + plan + state + `billing_anchor` + `Data` (external entity, tokens refs) |
| `Invoice` | Period charge; collection target |
| `BillingRun` | Period generation pipeline (flat and/or usage) |
| Profile clues (via checkout) | `paymentMethodId`, `providerCustomerId` |

**Payment method identity:** prefer reading profile clues (already written by checkout). Optionally copy last successful instrument onto `Subscription.Data` for multi-product isolation:

```json
{
  "externalEntityId": "cand_…",
  "externalEntityType": "candidate",
  "paymentMethodId": "pmd_…",
  "providerCustomerId": "cus_…",
  "provider": "flutterwave"
}
```

### 3.3 Collection result (product-facing, already exists)

```text
page_url | session_ref | invoice_id | subscription_id | already_complete
```

Products only need:

1. Redirect if `page_url` set  
2. `ConfirmPayment(session_ref)` on return  
3. Listen for lifecycle events for entitlement  

---

## 4. Make recurrent payment *trivial* (product API)

Products never implement rebill logic. They use three calls:

| Step | API | Product code |
|------|-----|----------------|
| Start | `CollectionService.StartSubscription` | One RPC; redirect to `page_url` |
| Confirm | `CollectionService.ConfirmPayment` | On return URL / poll |
| Cancel | `CollectionService.CancelSubscription` | Soft cancel (end of period) |

**Renewal is invisible to the product:** billing scheduler charges saved method, emits `subscription.billed` or dunning events; product updates cache only.

Pseudo-product (Opportunities):

```go
// Signup
res, _ := collection.StartSubscription(ctx, &StartSubscriptionRequest{
  ProfileId: candidateProfileID,
  PlanId: billingPlanID,
  CatalogVersionId: catalogVersionID,
  Currency: "USD",
  ReturnUrl: site + "/dashboard/?billing=success",
  Methods: []string{"card"},
  ExternalEntityId: candidateID,
  ExternalEntityType: "candidate",
})
if res.AlreadyComplete { /* free tier */ activateLocal(paid) }
else { redirect(res.PageUrl); stash(res.SessionRef, res.SubscriptionId) }

// Return
collection.ConfirmPayment(ctx, sessionRef)
// lifecycle worker also activates — either path is idempotent

// Cancel
collection.CancelSubscription(ctx, subscriptionID)
// product sets cancel_at_period_end from event or Confirm response
```

No Flutterwave types in product code.

---

## 5. Platform workstreams (phased)

### Phase A — Align signup with billing (foundation)

**Billing (mostly exists):**

- Provision Opportunities catalog version: plans Starter/Pro/Managed as **FLAT** monthly components (amounts match product catalog).
- Ensure `StartSubscription` + `ConfirmPayment` + settlement sweeper are live for the Opportunities partition.
- Lifecycle topic routed to Opportunities (`subscription.activated`, `subscription.billed`, `subscription.cancelled`).

**Opportunities:**

- Replace direct internal-checkout-only gateway with **billing Collection client**.
- Map product plan IDs → billing plan IDs (config table).
- On lifecycle events: update `candidate_profiles` (idempotent by `subscription_id` + event id).
- Keep `ConfirmPayment` on dashboard return as fast path; lifecycle is safety net.
- Deprecate product-local “period_end without billing” as SoT (mirror only).

**Exit criteria:** First payment creates billing subscription + paid invoice; product sees paid; no product-owned charge amounts.

---

### Phase B — Server-side collect with saved instrument (makes COF real)

**Billing (new, small, extensible):**

Add to Collection (or Billing) API:

```text
CollectInvoice(invoice_id, mode=auto|hosted, return_url?)
```

Behaviour:

1. Load invoice (must be `ISSUED`, non-zero).  
2. Resolve instrument:
   - `Subscription.Data.paymentMethodId` / `providerCustomerId`, else  
   - profile checkout clues.  
3. **If instrument present and mode=auto:**  
   - Call payment `InitiatePrompt` with portable extras:
     - `payment_method_id`, `customer_id`, `recurring=true`
     - `invoice_id`, `subscription_id`, `entity_type=prompt`  
   - Poll status (or wait for webhook → settle).  
   - On SUCCESS: same as `ConfirmPayment` settle path (invoice PAID, `subscription.billed`).  
   - On soft decline / 3DS required: fall back to **hosted** `CreateInvoiceCheckout` and return `page_url`.  
4. **If no instrument:** hosted checkout only (same as today).

**Checkout/payment (mostly exists):**

- Flutterwave `handleTokenCharge` already charges `payment_method_id`.  
- Ensure first interactive charge always persists clues (`payment_method_id`, `customer_id`) — already in checkout `writeClues` path; verify end-to-end on Opportunities card pay.

**Exit criteria:** Admin or test can `CollectInvoice` on an ISSUED invoice with no browser when card is on file.

---

### Phase C — Automatic renewal scheduler (recurrent by default)

**Billing (new worker / sweeper):**

`SubscriptionRenewalSweeper` (interval e.g. hourly, Trustage or in-process like settlement sweeper):

```
for each Subscription where state=ACTIVE
  and next_period_end <= now + lead_time
  and not cancel_at_period_end:

  1. Idempotency key: (subscription_id, period_start)
  2. Build period invoice:
       - Simple path: FLAT component re-rate for next month (recommended for Opportunities)
       - Full path: RunBilling(period) then IssueInvoice if lines exist
  3. IssueInvoice if DRAFT
  4. CollectInvoice(mode=auto)
  5. On PAID: advance billing_anchor / period; emit subscription.billed
  6. On FAIL: mark invoice OVERDUE; increment attempt; emit subscription.payment_failed
```

**Dunning policy (config, not hard-coded):**

| Attempt | Delay | Action |
|---------|-------|--------|
| 1 | day 0 | auto collect |
| 2 | day 3 | auto collect |
| 3 | day 7 | auto collect |
| exhaust | — | cancel or freeze; emit `subscription.past_due` / cancel |

**Cancel at period end:**

- Collection `CancelSubscription` sets cancel flag / end_at.  
- Sweeper skips rebill; after period end → `CANCELLED` + lifecycle event.  
- Matches current Opportunities UX (“access until end of period”).

**Exit criteria:** Active sub with saved card auto-pays next month without product code changes.

---

### Phase D — Product UX polish (Opportunities)

| UX | Source of truth |
|----|-----------------|
| Next charge date | Billing subscription period / next invoice |
| Card •••• last4 | Profile clues or invoice metadata |
| Update card | Hosted `CollectPayment` / Start re-auth checkout |
| Past due banner | Lifecycle `payment_failed` / local cache |
| Payment history | List invoices from billing (replace local checkout list long-term) |

---

## 6. Extensibility points (new products = config, not forks)

```text
Collector interface (billing internal)
  Collect(ctx, invoice) → { HostedURL?, PromptID?, Settled bool }

Implementations:
  • HostedCheckoutCollector   (card, MoMo via pay page)
  • SavedCardCollector        (Flutterwave token / Stripe PM / …)
  • ZeroAmountCollector       (already special-cased)

RenewalStrategy (per plan component)
  • FlatMonthly               (Opportunities)
  • UsageMetered              (RunBilling path)
  • Hybrid                    (base + usage)
```

Adding **Stripe** later: implement `SavedCardCollector` for Stripe + same sweeper.  
Adding **another product**: provision catalog + call `StartSubscription` + subscribe to lifecycle; **zero rebill code**.

---

## 7. Sequence diagrams

### First period (interactive)

```
User → Product: Choose plan
Product → Billing: StartSubscription
Billing → Checkout: Create session (invoice)
Billing → Product: page_url
User → pay.stawi.org: Pay card (3DS)
Checkout → Profile: clues (pmd, cus)
User → Product: return URL
Product → Billing: ConfirmPayment(session)
Billing: invoice PAID, sub ACTIVE
Billing → Queue: subscription.activated + billed
Product worker: candidate.subscription = paid
```

### Later periods (automatic)

```
Sweeper → Billing: due ACTIVE sub
Billing: create/issue period invoice
Billing → Payment: InitiatePrompt(pmd, cus, recurring)
Flutterwave: charge
Billing: settle PAID
Billing → Queue: subscription.billed
Product worker: extend entitlement / refresh period_end
```

---

## 8. Migration path from today’s Opportunities code

| Today | After plan |
|-------|------------|
| Local plan catalog + amounts | Billing catalog SoT; product map for labels only |
| Internal checkout session create | `StartSubscription` |
| Local `ActivateSubscription` only | Lifecycle + ConfirmPayment; local = cache |
| `current_period_end = now+1m` in product | Mirror from billing |
| Soft cancel in product repo | `CancelSubscription` + finalizer from events |
| No rebill | Phase B+C sweeper |

Keep Trustage `billing-reconcile` until ConfirmPayment + lifecycle are solid; then repurpose for dunning visibility only.

---

## 9. Implementation order (PR-sized)

| PR | Owner | Scope |
|----|--------|--------|
| **1** | Billing ops | Seed Opportunities catalog version + plans (FLAT) |
| **2** | Opportunities | Billing Collection client; StartSubscription for signup |
| **3** | Opportunities | Lifecycle consumer → entitlement cache |
| **4** | Checkout/FW | E2E verify pmd/cus written on first card pay |
| **5** | Billing | `CollectInvoice` / `CollectWithSavedInstrument` + settle |
| **6** | Billing | Flat period invoice builder (renew without full metering) |
| **7** | Billing | Renewal sweeper + dunning config |
| **8** | Opportunities | Billing history UI from invoices; past_due UX |
| **9** | Cleanup | Remove product-owned charge logic / dual period SoT |

PRs 1–4 get architecture right without silent charges.  
PRs 5–7 unlock “set and forget” recurring.  
PR 8–9 make product UX match.

---

## 10. Testing strategy

| Layer | Tests |
|-------|--------|
| Billing unit | StartSubscription → Confirm; CollectWithSavedInstrument happy/fail; sweeper idempotency |
| Billing integration | Real checkout testcontainer or fake; token charge mocked |
| Flutterwave | Token charge unit (already partially present) |
| Opportunities | Lifecycle handler table tests; no double activation |
| E2E sandbox | First pay → wait period (clock skew test) → silent renew |

**Idempotency keys:** `(subscription_id, period_start)` for renew invoices; ConfirmPayment already idempotent on session.

---

## 11. Risks and mitigations

| Risk | Mitigation |
|------|------------|
| 3DS on renew | Fall back to hosted collect; email “update payment” |
| Shared profile tokens across products | Prefer subscription-scoped Data; document multi-product |
| Catalog drift | Single catalog version pin in Opportunities config |
| Silent charge compliance | Explicit recurring consent on first checkout copy |
| Double charge | Unique invoice per period; refuse second collect if PAID |

---

## 12. Success definition

Recurring is **trivial** when:

1. A new product can go live with: **catalog rows + StartSubscription + lifecycle listener** — no PSP code.  
2. Opportunities monthly renew requires **no SPA involvement** if the card remains valid.  
3. Cancel at period end works without custom rebill skips in product (billing skips).  
4. Failed renewals surface as past_due / hosted recovery, not stuck “free” after paid.

---

## 13. Immediate recommendation

Do **not** build a product-side rebill cron that calls Flutterwave directly.

Do:

1. **Move signup onto `CollectionService.StartSubscription`** (Phase A).  
2. **Add one billing method: collect issued invoice with saved instrument** (Phase B).  
3. **Add renewal sweeper** that only creates invoices + calls that method (Phase C).

That reuses existing token charges, invoices, lifecycle events, and settlement patterns, and keeps every future product on the same path.
