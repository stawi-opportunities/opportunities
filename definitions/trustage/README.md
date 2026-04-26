# Trustage workflow definitions

These JSON files describe every scheduled job for opportunities that needs to run on a cron. Trustage reads them (via its workflow / schedule provisioning path — see below) and handles the cadence; our services stay pure workers.

## The workflows

| File | Cron | Calls | Purpose |
|---|---|---|---|
| `source-crawl-sweep.json` | `1m` | `POST /admin/crawl/dispatch-due` | Enumerates sources whose `next_crawl_at` is due and emits one `crawl.request` NATS message per source. The crawler replicas consume exactly-once via JetStream workqueue retention — no contention, no per-pod ticker. |
| `retention-expire.json` | `15m` | `POST /admin/retention/expire` | Flips `canonical_jobs.status` to `expired` for rows past `expires_at`. |
| `retention-mv-refresh.json` | `5m` | `POST /admin/retention/mv-refresh` | `REFRESH MATERIALIZED VIEW CONCURRENTLY mv_job_facets`. |
| `retention-stage2.json` | `24h` | `POST /admin/retention/stage2` | Physically deletes R2 snapshots past `RETENTION_GRACE_DAYS`. |
| `feeds-rebuild.json` | `3h` | `POST /admin/feeds/rebuild` | Regenerates `feeds/<cc>.json` + `feeds/default.json` + `feeds/index.json` on R2. The Cascade reads these for unfiltered first-paint so user sessions don't hit `/api/feed` for the default browse case. |

## Provisioning

All of these are meant to be idempotent. Create-or-replace a Trustage workflow whenever the file changes:

```bash
# Once Trustage exposes the CreateWorkflow + schedule-seed pipeline publicly.
# Until then, ops run this one-off after merging definition changes.
for f in definitions/trustage/*.json; do
  trustage-cli workflow apply --file "$f"
done
```

Each workflow carries a top-level `schedule` field that Trustage uses to seed a matching `schedule_definitions` row. Setting `schedule.active = false` and re-applying is the documented way to pause a cron.

## Why one `source-crawl-sweep` instead of per-source schedules

We considered per-source schedules (one `schedule_definitions` row per crawled site, ~100 → 10k rows over time). The sweep pattern instead fires a single minute-resolution workflow that fans out to NATS, and lets the crawler's workqueue consumer decide which replica handles each source. The trade-offs:

- **+** One workflow to author and monitor instead of N.
- **+** Source add/pause/disable already writes `sources.status` in Postgres; `ListDue` naturally respects that. No sync between `sources` and `schedule_definitions` to maintain.
- **+** JetStream workqueue retention gives exactly-once delivery to replicas with zero coordination code.
- **−** Trustage shows one workflow instance per minute, not per source. For per-source drill-down use the crawler's own logs / `admin/sources/health`.

If we later need per-source observability inside Trustage specifically (e.g. "show me every crawl attempt for source 42 with status codes"), we'd emit a `source.crawl.dispatched` event per message and a matching `source.crawl.result` event from the crawler — and a separate workflow reacting to those events. That's additive and doesn't change this file.

## Testing a single workflow outside Trustage

Every admin endpoint is a plain HTTP POST, so testing is `curl`:

```bash
# Inside the cluster:
kubectl run curl --image=curlimages/curl --rm -it --restart=Never -- \
  curl -fsS -XPOST http://opportunities-crawler.opportunities.svc/admin/crawl/dispatch-due
```

Expected body: `{"ok":true,"considered":N,"dispatched":N}`.
