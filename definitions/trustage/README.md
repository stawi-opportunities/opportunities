# Trustage workflows

These definitions schedule crawler maintenance and candidate notifications.
They call live PostgreSQL-backed service endpoints; no workflow publishes job
snapshots or maintains a secondary jobs store.

Crawler workflows cover overdue-source recovery, per-source schedule
reconciliation, crawl-run/watchdog recovery, source health and quality, and
recipe backfill (for deterministic extraction recipes — not crawl-time AI
stubs). Candidate workflows cover CV freshness and weekly match/job digests.
Billing reconcile polls pending checkouts every 2 minutes.

The crawler migration job synchronizes every JSON definition in this directory
through Trustage. Definitions and their schedules are idempotent.

## Authentication

Matching `/_admin/*` endpoints require either:

- `X-Admin-Token: <ADMIN_SHARED_SECRET>` (preferred for Trustage), or
- a Bearer JWT with the `admin` role.

Set `ADMIN_SHARED_SECRET` on the matching service and inject the same value
into Trustage workflow secrets so `${ADMIN_SHARED_SECRET}` in the JSON headers
resolves. Without a secret (or JWT), admin routes fail closed (401).
