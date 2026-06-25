# Crawl Pipeline — System Map

How a job posting goes from "exists on a source website" to "served to users",
and every mechanism that keeps that loop running. Verified against the code on
2026-06-11 (post #72, event-driven per-source schedules).

**Owner:** Platform / Peter Bwire

---

## 1. The loop at a glance

```
Trustage per-source cron (opportunities.source.crawl.{id})
  └─ POST /admin/sources/{id}/crawl ......... source_crawl_handler.go:39
       ├─ status guard (active/degraded only)
       ├─ backpressure Gate.Admit → 429 + Retry-After when saturated
       └─ emit crawl.requests.v1 (idempotency_key = {id}:{tick minute})
            └─ CrawlRequestHandler.Execute ... crawl_request_handler.go:177
                 ├─ crawl_jobs row (unique idempotency_key dedups NATS redelivery)
                 ├─ connector or recipe iterator, checkpoint resume (≤6h old)
                 ├─ per page: archive raw HTML → R2, raw_payloads row,
                 │            enrich stubs → opportunity.Verify →
                 │            normalize → variants.ingested.v1 (or crawl_inbox)
                 ├─ per page: checkpoint Put; cleared only on clean finish
                 └─ emit crawl.page.completed.v1 + crawl_jobs Finish
                      └─ PageCompletedHandler .. page_completed_handler.go:74
                           ├─ health_score ±, consecutive_failures, needs_tuning
                           ├─ next_crawl_at = now + crawl_interval_sec
                           │   (informational only — Trustage cron drives timing)
                           └─ reject-rate drift → recipe.regenerate.v1
```

Downstream (worker/frontier-worker/materializer):

```
variants.ingested.v1 → normalize → validate → dedup (hard_key → cluster_id,
Valkey+Postgres) → canonical merge (opportunities UPSERT, sparse
newest-non-empty-wins) → publish (R2 {kind}/{slug}.json + PublishedV1 →
search index / Iceberg)
```

Key dedup invariant: `hard_key = BuildHardKey(company, title, location,
externalID)`; same hard_key ⇒ same cluster ⇒ one `opportunities` row.
Re-crawls are idempotent end to end: `first_seen_at` sticks, `last_seen_at`
advances, blank fields never overwrite populated ones
(pkg/variantstate/store.go:564).

## 2. Scheduling model (post #72)

There is **no central dispatcher**. Each active/degraded source owns a Trustage
workflow `opportunities.source.crawl.{sourceID}` whose cron is derived from
`crawl_interval_sec` with per-source jitter (source_schedules.go:64). The
legacy static sweep (`opportunities.source.crawl-tick` →
`/admin/sources/crawl-due`) is retired and actively archived at boot
(cmd/main.go:587-594); the endpoint no longer exists.

Schedules are kept in sync with `sources.status` three ways:

1. **Per-mutation**: pause/enable/stop endpoints emit
   `sources.scheduling.changed.v1`; the handler re-derives desired state from
   the *live* row (not event intent), so it is idempotent and order-independent
   (source_scheduling_handler.go:75).
2. **Boot reconcile**: detached goroutine, 5-min budget; failure is a WARN log
   only (cmd/main.go:582-601).
3. **Backstop cron**: `opportunities.source.schedule.reconcile` every 10 min →
   `POST /admin/sources/schedules/reconcile`
   (definitions/trustage/source-schedule-reconcile.json).

> ⚠️ All three mechanisms — and every crawl tick — depend on Trustage. The
> backstop that heals Trustage drift is itself a Trustage cron. See §5.

`sources.next_crawl_at` is still stamped by PageCompletedHandler but is only
consumed by `GET /admin/sources/due` (observability); it does not drive
dispatch.

## 3. Fetch & enumeration

Connector selected by `source.Type` from the registry, unless
`RECIPE_ENABLED` and the source has an active `extraction_recipe` — then the
deterministic recipe executor runs instead (crawl_request_handler.go:248).

| Path | Pagination | Checkpoint/resume | Notes |
|---|---|---|---|
| greenhouse / workday / smartrecruiters | single page | yes (resume ⇒ done) | page++ happens before Checkpoint(); semantics are "next page", works but inverted |
| recipe executor (API) | page_param / cursor, maxPages cap | **no** — redelivery restarts at page 1 | cursor no-progress guard stops loops |
| recipe executor (HTML) | next_link / page_param | **no** | detail-page fetch failures are silently skipped (executor.go:93-98) |
| spec jsonfeed | cursor | no | stop-on-empty configurable |
| sitemapcrawler | robots.txt → sitemaps → URL batches of 50 | no | |
| universal | sitemap first, then AI link discovery, maxPages 50 | no | fallback for recipe-eligible types |

HTTP layer (pkg/connectors/httpx): 5 attempts, retry on 429/5xx, exp backoff
200ms→30s ±30% jitter, 90s budget, 10MB body cap. WAF blocks (403/429/451/503)
fall back to the Bright Data unblocker proxy (httpx/fallback.go); direct
success never pays for the proxy.

Checkpoints (`crawl_checkpoints`, keyed source_id+connector_type) are written
per page inside the loop and **cleared only on clean completion**; an iterator
error leaves the row so NATS redelivery resumes from the failed page. Stale
rows (>6h) are ignored. Manual relief:
`DELETE /admin/checkpoints/{src}/{type}`.

## 4. Source lifecycle

```
discovered (sources.discovered.v1, sampled from crawl pages)
  → classify (ATS path match, else generic_html) → blocklist check
  → SourcePending  → verify (sourceverify) → SourceVerified → operator approve
  → SourceActive ⇄ SourceDegraded   (crawling allowed only in these two)
  → SourcePaused / SourceDisabled / SourceBlocked  (schedule archived)
```

Seeds (`seeds/*.json`) load directly as Active at boot. Health:
error ⇒ −0.2 + consecutive_failures++; success ⇒ +0.1; reject-rate >0.8 ⇒
`needs_tuning=true` (excludes the source from recipe backfill until manually
cleared). Hourly `sources-health-decay` cron nudges +0.05 toward 1.0.
**Nothing automatically demotes a failing source** — health 0 still crawls.

Recipe backfill: 15-min Trustage cron → `/admin/recipes/backfill` → queue
`recipe.generate.v1` for recipe-eligible, recipe-less, not-needs-tuning
sources; generated recipes activate when pass_rate ≥ threshold, otherwise the
source is flagged `needs_tuning`.

## 5. Gap history (audited + fixed 2026-06-11)

The 2026-06-11 audit found these; the same-day hardening pass (branch
fix/crawl-correctness) closed 1-4 and 6. Kept for incident archaeology.

1. **[FIXED] Thundering herd + denied tick = 12h of lost crawling.**
   `cronForInterval` emitted `"M */12 * * *"` — all ~90 sources fired in
   hours 0 and 12 UTC; a closed gate during that window 429'd the whole
   fleet (exactly the 2026-06-11 outage). Fixed two ways: the hour phase
   is now jittered per source (source_schedules.go cronForInterval), and
   the static-synced `opportunities.source.crawl-overdue` sweep
   re-dispatches anything ≥1h past due, gate-permitting
   (CrawlOverdueHandler). The sweep also covers the 2026-06-03 mode
   (dynamic workflows not firing) and Trustage downtime.
2. **[FIXED] No detection for "crawling stopped".** Now: OTel counters
   `crawl.dispatch.total` (emitted/denied/skipped × schedule/overdue),
   `crawl.completed.total`, `crawl.silent_loss.total`,
   `crawl.source.health.total`, `retention.jobs.expired`
   (pkg/telemetry/crawl.go) + the hourly `opportunities.crawl.watchdog`
   cron hitting /admin/crawl/watchdog, which ERROR-logs when sources are
   due but nothing has crawled in 3h.
3. **[FIXED] No stale-job reconciliation.** /admin/retention/expire now
   hides jobs not re-seen for max(3×interval, 7d) — guarded so it only
   fires when the source HAS crawled since (no mass-expiry during crawl
   outages) — and restores sweep-hidden jobs that reappear. The
   retention-expire Trustage cron is live.
4. **[FIXED] No source demotion / stuck needs_tuning.** 5 consecutive
   failures auto-demote active→degraded (still crawled, loudly visible);
   recovery auto-promotes back. needs_tuning now expires after 7d
   (NeedsTuningTTL) so recipe backfill retries instead of skipping
   forever.
5. **Recipe crawls don't checkpoint** — every redelivery re-fetches from
   page 1 (duplicate fetch cost; dedup catches the duplicates
   downstream). Open; bounded cost.
6. **[INSTRUMENTED] Silent partial loss**: recipe-HTML detail fetch
   skips, variant publish/inbox drops, and empty raw_archive_ref now
   count into `crawl.silent_loss.total` (and detail skips WARN-log per
   listing). Drops still self-heal on the next crawl cycle.
7. **Status/schedule divergence window**: pause/stop flips Postgres then
   emits an event; if the emit or Trustage archive fails, the source
   keeps crawling until the 10-min reconcile heals it. Open; bounded to
   10 min.

Separate from the crawler: the 2026-06-11 outage's root cause was worker
consumers configured with `ack_wait=30s, max_ack_pending=64` while stage
handlers took ~4s/message — ~70% redelivery churn, ~20 msgs/min drain
against an 842k backlog. Fixed in deployment.manifests (300s/1024).

## 6. Operator quick reference

| Action | How |
|---|---|
| Force a crawl now | `POST /admin/sources/{id}/crawl` |
| Catch up overdue sources | `POST /admin/sources/crawl-overdue` |
| Pipeline liveness check | `GET /admin/crawl/watchdog` |
| Stale-job sweep | `POST /admin/retention/expire` |
| List sources past due | `GET /admin/sources/due` |
| Heal schedule drift | `POST /admin/sources/schedules/reconcile` |
| Backpressure gate state | `GET /admin/crawl/status` |
| Crawl audit ledger | `GET /admin/crawl_jobs` |
| Stuck checkpoint | `DELETE /admin/checkpoints/{src}/{type}` |
| Re-extract stored HTML | `POST /admin/raw_payloads/{id}/reparse` |
| Pause / enable / stop source | `POST /admin/sources/{pause,enable,stop}?id=` |
| Health snapshot | `GET /admin/sources/health` |
