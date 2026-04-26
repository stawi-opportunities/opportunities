# Billing provisioning

Stawi.jobs delegates payment + subscription lifecycle to the
antinvestor `service_payment` (owns rails + gateways) and
`service_billing` (owns catalog + subscriptions + invoices). This
document describes the one-time setup needed per environment
(staging / production) so `/billing/checkout` works end-to-end.

## Flow recap

```
Website (stawi.opportunities)
    │ POST /billing/checkout
    ▼
candidates service
    │ 1. BillingService.CreateSubscription   (PENDING)
    │ 2. PaymentService.InitiatePrompt       (extras: subscription_id, product_id, success_url)
    │ 3. short-poll Status(prompt_id) for checkout_url OR SUCCESSFUL
    ▼
service_payment
    │ routes by `route` string (POLAR / M-PESA / AIRTEL / MTN)
    ▼
integration app (apps/integrations/polar,mpesa,airtel,mtn)
    │ creates real provider session (Polar hosted checkout / M-Pesa STK push / …)
    │ on webhook — updates prompt Status via StatusUpdate
    ▼
candidates reconciler (every 30s, also inline when status flips to "paid")
    │ GetSubscription(subscription_id)
    │ if state=SUBSCRIPTION_ACTIVE → candidate.Subscription="paid", AutoApply=true
```

## One-time catalog bootstrap (service_billing)

Run once per environment, ideally via a seed job. Requires a
`service_billing:catalog_manage` role.

1. Create a CatalogVersion:

   ```
   BillingService.CreateCatalogVersion({
     id: "opportunities-v1",       // matches env BILLING_CATALOG_VERSION_ID
     catalog_id: "opportunities",
     name: "Stawi Jobs v1",
     currency: "USD"
   })
   ```

2. Create three Plans with `external_id` = tier id (this is what
   stawi passes as `plan_id` in CreateSubscriptionRequest):

   | external_id | name    | amount (USD) |
   | ----------- | ------- | ------------ |
   | `starter`   | Starter | 10           |
   | `pro`       | Pro     | 50           |
   | `managed`   | Managed | 200          |

3. Publish the catalog version (`PublishCatalogVersion`).

Plans currently ship without usage components — the MVP is flat-fee
monthly. Usage components can be added later for future "per-CV-score"
or "per-match-delivered" pricing without changing stawi code.

## Per-environment configuration (candidates service)

```yaml
# Candidates-service env (staging → k8s Secret; prod → Vault)
BILLING_SERVICE_URI: payment.antinvestor.svc.cluster.local:50051
BILLING_CATALOG_VERSION_ID: opportunities-v1
BILLING_RECIPIENT_PROFILE_ID: <profile id of the stawi.opportunities merchant in service_profile>

# Polar.sh — one product per tier created in polar.sh dashboard.
# See https://docs.polar.sh/api/products
POLAR_PRODUCT_STARTER: prod_xxx
POLAR_PRODUCT_PRO:     prod_yyy
POLAR_PRODUCT_MANAGED: prod_zzz

# Public site used for redirect URLs.
PUBLIC_SITE_URL: https://opportunities.stawi.org

# Reconciler — how often to sync PENDING candidates from service_billing.
# 0 disables the reconciler (not recommended for production).
BILLING_RECONCILE_INTERVAL: 30s
```

## Per-provider configuration (service_payment)

These are NOT on stawi's side — they live with the payment service.
For completeness:

- **Polar.sh**: `POLAR_API_KEY`, `POLAR_WEBHOOK_SECRET`,
  `POLAR_ORGANIZATION_ID` on the polar integration app. The
  webhook URL registered in the Polar dashboard points at
  `service_payment`'s `/webhook/polar` handler, not stawi.
- **M-Pesa / Airtel / MTN**: per-telco creds in each integration app;
  same pattern.

Stawi never sees these secrets. The `route` string on InitiatePrompt
is the only selector.

## What ops must watch

- `stawi.opportunities.candidates` logs for `billing-reconciler` events —
  steady-state is "no pending" most ticks. A growing pending count
  means service_payment → stawi subscription sync is broken.
- service_payment prompt dashboard — checkout_url materialisation
  should land within ~2s of the InitiatePrompt. If it stalls,
  stawi returns `status: "pending"` to the frontend and the user
  sees a spinner.
- Duplicate subscription rows — stawi upserts by profile_id, but if
  a user repeatedly clicks "Subscribe" while pending, fresh
  service_billing subscriptions are created each time. The
  reconciler will activate the most-recent one, but cancelled
  stale rows pile up. Consider a janitor job later.

## Smoke test

```bash
# 1. Get JWT for a test profile
TOKEN=$(…)

# 2. Hit plans catalog (unauthenticated)
curl -s https://api.stawi.org/billing/plans | jq

# 3. Trigger checkout
curl -s -X POST https://api.stawi.org/billing/checkout \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -H "CF-IPCountry: KE" \
  -d '{"plan_id":"pro","phone":"+254712345678","route_hint":"mpesa"}' | jq

# Expect status=pending, prompt_id=…
# User's phone receives M-Pesa STK push.

# 4. Poll status (frontend does this automatically)
curl -s "https://api.stawi.org/billing/checkout/status?prompt_id=<id>" \
  -H "Authorization: Bearer $TOKEN" | jq

# Expect status=paid within ~90s of user confirming the prompt.
```
