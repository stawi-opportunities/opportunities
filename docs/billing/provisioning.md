# Billing provisioning

Stawi Opportunities delegates payment rails to antinvestor
`service_payment` (+ integrations polar/mpesa/airtel/mtn/…). Subscription
entitlement lives on `candidate_profiles` (flipped free→paid by matching's
activator). Matching owns a local checkout ledger (`candidate_checkouts`)
for poll/webhook/reconcile.

## Happy path

```
SPA (opportunities.stawi.org)
    │ POST /matching/billing/checkout  { plan_id, email?, phone? }
    ▼
opportunities-matching
    │ 1. Validate plan (starter|pro|managed)
    │ 2. Gateway.CreateCheckout
    │    • route = RouteForCountry(CF-IPCountry)  # KE→mpesa, US→polar, …
    │    • PaymentService.InitiatePrompt(
    │        route=mpesa|polar|…,                # service-payment route keys
    │        id=chk_…,
    │        extra={ product_id?, success_url, customer_email, plan_id },
    │        source.contact_id = phone           # M-Pesa reads ContactId
    │      )
    │    • Polar: short-poll Status(entity_type=prompt) for checkout_url
    │ 3. Persist candidate_checkouts row (pending)
    │ 4. Return { status: redirect|pending|paid, prompt_id, redirect_url }
    ▼
service-payment
    │ publishes InitiatePrompt to INITIATE_PROMPT_ROUTE_URIS[route]
    │   polar  → svc.payment.integration.polar.prompts
    │   mpesa  → svc.payment.integration.mpesa.prompts
    ▼
integration (polar / mpesa / …)
    │ Polar: CreateCheckout session → StatusUpdate(checkout_url)
    │ M-Pesa: STK push → webhook → StatusUpdate(SUCCESSFUL|FAILED)
    ▼
matching activation (any of)
    │ GET  /matching/billing/checkout/status  (inline Activate on paid)
    │ POST /matching/billing/webhook          (HMAC X-Payment-Signature)
    │ POST /_admin/billing/reconcile          (Trustage sweep)
    ▼
candidate_profiles.subscription = paid, plan_id set, AutoApply from entitlements
```

## UI

| Step | Behaviour |
|------|-----------|
| Onboarding “Continue to payment” | `createCheckout` → redirect URL or `/dashboard/?billing=pending&prompt_id=` |
| Dashboard `PendingCheckoutPoller` | Polls status every 4s; opens `redirect_url` if Polar is late; celebrates on paid |
| `CompletePaymentPanel` | Retry checkout for unpaid / past_due |

## Matching env (production)

```yaml
BILLING_SERVICE_URI: http://service-payment.finance.svc:80
BILLING_WEBHOOK_SECRET: <vault billing-credentials-opportunities>
PUBLIC_SITE_URL: https://opportunities.stawi.org
# Required for card/Polar countries (prod_… from Polar dashboard)
POLAR_PRODUCT_STARTER: prod_xxx
POLAR_PRODUCT_PRO: prod_yyy
POLAR_PRODUCT_MANAGED: prod_zzz
```

OAuth: matching requests `/payment` (and `/billing`) audiences so the
service-to-service JWT is accepted by service-payment.

## service-payment env (finance)

```yaml
INITIATE_PROMPT_TOPIC_URI: …mpesa.prompts   # default rail
INITIATE_PROMPT_ROUTE_URIS: |
  {
    "mpesa": "…mpesa.prompts",
    "m-pesa": "…mpesa.prompts",
    "polar": "…polar.prompts",
    "mtn": "…mtn.prompts",
    "airtel": "…airtel.prompts",
    ...
  }
```

Matching sends provider route keys (`mpesa`, `polar`, …). UI responses
still use display routes (`M-PESA`, `POLAR`).

## Polar products

Create one Polar product per tier (monthly USD). Put product ids in
matching env (or Vault later). Without them, card-country checkouts
reach Polar without `product_id` and the integration fails.

## Smoke test

```bash
# Plans (public)
curl -s https://api.stawi.org/matching/billing/plans | jq

# Checkout (auth'd) — Kenya STK
curl -s -X POST https://api.stawi.org/matching/billing/checkout \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -H "CF-IPCountry: KE" \
  -d '{"plan_id":"pro","phone":"+254712345678"}' | jq
# expect status=pending, prompt_id=chk_…

# Checkout — card country (needs POLAR_PRODUCT_*)
curl -s -X POST https://api.stawi.org/matching/billing/checkout \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -H "CF-IPCountry: US" \
  -d '{"plan_id":"pro","email":"you@example.com"}' | jq
# expect status=redirect + redirect_url, or pending then poll

# Poll
curl -s "https://api.stawi.org/matching/billing/checkout/status?prompt_id=$PID" \
  -H "Authorization: Bearer $TOKEN" | jq
```

## Ops watch

- Matching log: `billing: payment gateway enabled`
- Growing `candidate_checkouts` pending count → integration/webhook gap
- Polar: `checkout_url` should appear within ~2s of InitiatePrompt
- M-Pesa: STK on phone; confirm shortcode credentials in settings
