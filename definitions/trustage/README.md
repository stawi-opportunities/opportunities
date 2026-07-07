# Trustage workflows

These definitions schedule crawler maintenance and candidate notifications.
They call live PostgreSQL-backed service endpoints; no workflow publishes job
snapshots or maintains a secondary jobs store.

Crawler workflows cover overdue-source recovery, per-source schedule
reconciliation, crawl-run/watchdog recovery, source health and quality, and
recipe backfill. Candidate workflows cover CV freshness and weekly match/job
digests.

The crawler migration job synchronizes every JSON definition in this directory
through Trustage. Definitions and their schedules are idempotent.
