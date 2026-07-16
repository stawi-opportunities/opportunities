# Trustage workflows

These definitions schedule crawler maintenance and candidate notifications.
They call live PostgreSQL-backed service endpoints; no workflow publishes job
snapshots or maintains a secondary jobs store.

Crawler workflows cover overdue-source recovery, per-source schedule
reconciliation, crawl-run/watchdog recovery, source health and quality, and
recipe backfill (for deterministic extraction recipes — not crawl-time AI
stubs). Candidate workflows cover CV freshness and weekly match/job digests.

### Billing (automatic recurring + settlement)

**Per-entity Trustage one-shots** created by service-billing — **no bulk scans**.

#### Renewals (`billing.subscription.renew.{subscriptionId}`)

| When | What billing does |
|------|-------------------|
| First successful pay | Ensure one-shot at `periodEnd − lead` |
| COF success | Re-arm for next period |
| COF failure | Re-arm at next dunning slot (`0,24,72,168`h) |
| Soft cancel | Finalize one-shot at `periodEnd` |
| Max attempts / hard cancel | **Archive** workflow |

Fire → `POST …/subscriptions/{id}/renew` (that sub only).

#### Settlement (`billing.invoice.settle.{invoiceId}`)

| When | What billing does |
|------|-------------------|
| Checkout opens | Ensure first poll (~2m; configurable) |
| Session not completed | Re-arm next delay (`2,5,15,30,60,120`m) |
| Settled / paid / voided | **Archive** workflow |
| Max attempts | **Archive** (stop polling) |

Fire → `POST …/invoices/{id}/settle` (that invoice only).

| Static definition | Schedule | Target |
|-------------------|----------|--------|
| `billing-reconcile.json` | `*/2 * * * *` | Matching pending **product** checkout activation |
| `billing-settle.json` | *(none)* | Doc-only; real settle workflows are dynamic per invoice |

Dunning renew: `BILLING_RENEWAL_RETRY_DELAYS_HOURS` / `BILLING_RENEWAL_MAX_ATTEMPTS`.  
Settle polls: `BILLING_SETTLEMENT_RETRY_DELAYS_MINUTES` / `BILLING_SETTLEMENT_MAX_ATTEMPTS`.  
Flutterwave **v4 OAuth only** for COF renewals.

The crawler migration job synchronizes every JSON definition in this directory
through Trustage. Definitions and their schedules are idempotent.

## Authentication

Matching `/_admin/*` endpoints require either:

- `X-Admin-Token: <ADMIN_SHARED_SECRET>` (preferred for Trustage), or
- a Bearer JWT with the `admin` role.

Set `ADMIN_SHARED_SECRET` on the matching service and inject the same value
into Trustage workflow secrets so `${ADMIN_SHARED_SECRET}` in the JSON headers
resolves. Without a secret (or JWT), admin routes fail closed (401).

Billing internal routes require:

- `X-Admin-Token: <BILLING_INTERNAL_ADMIN_TOKEN>` (or Bearer), and
- Trustage secrets: `BILLING_INTERNAL_URL` (e.g. `http://service-payment-billing.finance.svc:80`),
  `BILLING_INTERNAL_ADMIN_TOKEN` matching the billing service env.
