# Billing provisioning

Opportunities checkout is **Flutterwave only**. No geo-routing, Polar, or
direct MoMo rails. Subscription entitlement is flipped free→paid on
`candidate_profiles` after payment confirms.

## Required steps (happy path)

```
1. SPA  POST /matching/billing/checkout  { plan_id, email? }
2. matching
     • validate plan (starter|pro|managed)
     • InitiatePrompt(route=flutterwave, id=chk_…)
     • short-poll Status until extras.checkout_url
     • persist candidate_checkouts (pending)
     • return { status: redirect, redirect_url, prompt_id }
3. SPA  window.location → Flutterwave checkout_url
4. User pays on Flutterwave
5. Flutterwave redirects →
     /dashboard/?billing=success&prompt_id=chk_…
6. Activation (any one is enough; all are idempotent)
     • POST /billing/webhook  (HMAC)
     • GET  /billing/checkout/status  (inline Activate on paid)
     • POST /_admin/billing/reconcile  (Trustage sweep)
7. SPA celebrates + refreshes subscription → dashboard active
```

### Recovery only (not the happy path)

| Case | Behaviour |
|------|-----------|
| `checkout_url` not ready in short-poll | SPA → `?billing=pending&prompt_id=` and polls until URL or terminal |
| Pay failed | `?billing=failed` + Retry (new checkout) |
| Webhook delayed on return | Poller uses `prompt_id` from success URL and status endpoint until paid |

## Matching env

```yaml
BILLING_SERVICE_URI: http://service-payment.finance.svc:80
BILLING_WEBHOOK_SECRET: <vault>
PUBLIC_SITE_URL: https://opportunities.stawi.org
```

Flutterwave credentials live on `service-payment-flutterwave` (finance), not
matching. Matching only needs the payment service URI + webhook secret.

## Smoke test

```bash
curl -s -X POST https://api.stawi.org/matching/billing/checkout \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"plan_id":"managed","email":"you@example.com"}' | jq
# expect: status=redirect, redirect_url=https://…, prompt_id=chk_…

curl -s "https://api.stawi.org/matching/billing/checkout/status?prompt_id=$PID" \
  -H "Authorization: Bearer $TOKEN" | jq
```

## Ops

- Boot log: `billing: payment gateway enabled (flutterwave only)`
- Pending rows growing → webhook / Flutterwave worker gap
- `checkout_url` should appear within ~2s of InitiatePrompt
