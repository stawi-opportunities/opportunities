# Trustage workflows

These definitions schedule crawler maintenance and candidate notifications.
They call live PostgreSQL-backed service endpoints; no workflow publishes job
snapshots or maintains a secondary jobs store.

Crawler workflows cover overdue-source recovery, per-source schedule
reconciliation, crawl-run/watchdog recovery, source health and quality, and
recipe backfill (for deterministic extraction recipes — not crawl-time AI
stubs). Candidate workflows cover CV freshness and weekly match/job digests.

### Billing (automatic recurring)

**Preferred model: one Trustage reminder per subscription** (created by
service-billing — not a global “scan all subs” cron).

| When | What billing does |
|------|-------------------|
| First successful pay | Create/activate `billing.subscription.renew.{id}` one-shot at `periodEnd − lead` |
| COF success | Advance period; re-arm one-shot for next period |
| COF failure | Increment attempt; re-arm at next dunning slot (`0,24,72,168`h) |
| Soft cancel | Replace rebill with finalize one-shot at `periodEnd` |
| Max attempts / hard cancel | **Archive** the Trustage workflow (no more fires) |

Trustage fires → `POST {BILLING_INTERNAL_URL}/_internal/billing/subscriptions/{id}/renew`
(processes **only that** subscription, then re-plans from properties).

| Static definition | Schedule | Target |
|-------------------|----------|--------|
| `billing-reconcile.json` | `*/2 * * * *` | Matching pending checkout activation safety net |
| `billing-settle.json` | `*/2 * * * *` | Billing settle abandoned hosted sessions |

There is **no** bulk subscription renew cron. Every rebill is a per-sub Trustage
workflow created by service-billing.

Settlement (hosted checkout abandon recovery) may use an in-process ticker
(default 60s) or Trustage settle — that path is invoice/session scoped, not
“scan all subscriptions”.

Dunning: `BILLING_RENEWAL_RETRY_DELAYS_HOURS` (default `0,24,72,168`) and
`BILLING_RENEWAL_MAX_ATTEMPTS` (default 4). Flutterwave **v4 OAuth only** for COF.

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
