# Matching pipeline (production)

**Product model (2026-08):** **invoke-only** matching with a **quality floor**. Match rows are not monetized by count; paid plans differ by **daily invoke ceilings** and **digest cadence**.

## Paths

| Path | Trigger | Action | User notification |
|------|---------|--------|-------------------|
| **A FanOut** | Opportunity embed succeeds | Reverse-KNN → score → `candidate_matches` | Optional `Send` when `match_alerts=true` |
| **C Index-only** | Persona/CV embedding event | Upsert `candidate_match_indexes` only — **no auto gap-fill** | None |
| **MatchInvoke** (`user_refresh`) | `POST /me/matches/refresh` (“Find matches”) | Shared `MatchInvoke` KNN + score ≥ floor → upsert | In-app only (no every-match email) |
| **MatchInvoke** (`digest`) | Trustage paid match-digest cron | `MatchInvoke` then top-**3 unseen** with receipts | Always `Send` (`matches.digest`) when ≥1 eligible |
| **Preference / profile** | Preferences updated | May refresh vectors/index; matches wait for next invoke | — |
| **Weekly jobs digest** | Trustage free re-engagement | Market summary (not personalized MatchInvoke) | `weekly_jobs.digest` |
| **CV stale** | Trustage cron | Nudge for old CV | Always `Send` (`cv.stale_nudge`) |

### Defaults

| Path | Default | Notes |
|------|---------|--------|
| **Path A** | **OFF** (`MATCHING_FANOUT_ENABLED=false`) | Kill-switch may stay for emergency re-enable; not the product default |
| **Path C** | **Index-only** | Consumer still runs; does **not** write match rows on embed |
| **Match generation** | **User refresh + digest only** | Single entry point: `matching.MatchInvoke` |

### Matching persona (candidate side)

Every chat turn rebuilds a **Matching persona v1** document:

1. **Intent** — rolling conversation digest (user turns, not full transcript)
2. **Preferences** — structured fields (role, markets, salary, types)
3. **Qualifications** — CV / skills

Stored on `candidate_placement_profiles` (`summary_text`, `conversation_digest`).  
Embedded into `candidate_match_indexes` with `rerank_text` for stage-2 / invoke scoring.

**Dual-writer rule:** persona embeds (`source=persona`) own the index. Thin async CV-field embeds (`source=cv_fields`) cannot overwrite when `rerank_text` is set.

**Rule:** matching never sends email/SMS itself. Delivery uses the same
constructs as **service-profile**:

1. `connection.NewServiceClient` → `notificationv1connect.NotificationServiceClient`
2. Build `notificationv1.Notification` with `Template`, `Payload` (`structpb`),
   `Recipient` (`ContactLink` with `ProfileType` + `ProfileId`), `OutBound`,
   `AutoRelease`
3. `NotificationService.Send` and drain the stream (`pkg/notify.Send`)

Domain events on the matching bus remain for analytics/bridges only.

Default UX: **invoke on demand; digest on schedule**. Real-time every-match
send remains opt-in via Settings → “Notify on every match” (`match_alerts`)
and only applies if Path A (or another live generator) is enabled.

## Scoring

All invoke paths use the same cosine term (`CosineFromPGDistance` / blend weights):

- Cosine 0.60, Skills 0.15, Geo 0.15, Salary 0.10, Stale −0.10  
- Floor: `MATCHING_MIN_SCORE` (default **0.70** / 70% display)  
- **No plan match-row caps** — feed shows all active matches above the floor  
- Legacy overflow / daily-weekly cap columns may exist in schema but are **not** product monetization

## MatchInvoke & invoke limits

| Reason | Source | Consumes daily invoke budget? | Notification |
|--------|--------|-------------------------------|--------------|
| `user_refresh` | `POST /me/matches/refresh` | Yes | No |
| `digest` | Admin / Trustage paid digest | No | Yes (top-3 unseen) |
| `onboard_seed` (optional) | Post-index seed | Yes (or counts as free daily) | No |

| Tier | Default invokes / UTC day | Env |
|------|---------------------------|-----|
| Free (unpaid) | **1** | `MATCHING_INVOKE_LIMIT_FREE` |
| Starter | **30** | `MATCHING_INVOKE_LIMIT_STARTER` |
| Managed | **100** | `MATCHING_INVOKE_LIMIT_MANAGED` |

Exceeded user-facing invoke → `rate_limited` (no KNN). Digest invokes never
decrement the user budget.

Unpaid subscription still maps to free entitlements even if `plan_id` was set
at onboard. Paid period end without rebill demotes to free
(`FinalizeExpiredPaidAccess` in billing reconcile).

## Digests

| Audience | Definition | Content |
|----------|------------|---------|
| Paid / past_due / trial | `candidates-matches-weekly-digest` | After `MatchInvoke(digest)`, email **≤3 highest-scoring unseen** matches; notification **receipts** suppress repeats |
| Free | `candidates-weekly-jobs-digest` | Market / jobs summary (not personalized match rows) |

**Paid cadence** (`email_digest` on profile, Settings → Notifications):

| Value | Behavior (under `DIGEST_DEFAULT_CADENCE=auto`) |
|-------|--------------------------------------------------|
| `off` | No match digests |
| `twice_daily` | Local hours **[8,10)** and **[17,19)** (`DIGEST_TIMEZONE`) |
| `daily` | Every digest run that reaches the user |
| `weekly` | Only on `DIGEST_WEEKLY_WEEKDAY` in `DIGEST_TIMEZONE` |

`PUT /me/notifications` normalizes cadence via `matching.NormalizeDigestCadence`
(accepts `twice_daily`, `twice-daily`, `bidaily`, etc.). Weekly summary toggle
and email channel also gate delivery.

Trustage match-digest cron should fire **at least hourly** so twice_daily
windows work across timezones (see `definitions/trustage/README.md`).

## Deploy env

### Matching

| Env | Default | Purpose |
|-----|---------|---------|
| `NOTIFICATION_SERVICE_URI` | — | service-notification base URL |
| `NOTIFICATION_SERVICE_WORKLOAD_API_TARGET_PATH` | `/ns/notifications/sa/service-notification` | SPIFFE path (profile-style) |
| `MESSAGE_TEMPLATE_MATCHES_READY` | `template.opportunities.matches.ready` | Per-match alert template |
| `MESSAGE_TEMPLATE_MATCHES_DIGEST` | `template.opportunities.matches.digest` | Paid match digest |
| `MESSAGE_TEMPLATE_WEEKLY_JOBS_DIGEST` | `template.opportunities.weekly_jobs.digest` | Free jobs summary |
| `MESSAGE_TEMPLATE_CV_STALE_NUDGE` | `template.opportunities.cv.stale_nudge` | CV freshness |
| `MESSAGE_TEMPLATE_ATS_REPORT` | `template.opportunities.cv.ats_report` | Paid ATS report email |

**Template registration:** catalog in `pkg/notify/catalog.go`. Setup/migrate Job
(`DO_DATABASE_MIGRATE=true` + `NOTIFICATION_SERVICE_URI`) runs
`notify.EnsureFromConfig` so missing templates are created via
`TemplateSave`. See `definitions/notification-templates/README.md`.
| `MATCHING_FANOUT_ENABLED` | **`false`** | Path A consumer (invoke-only product default) |
| `OPPORTUNITY_FANOUT_QUEUE_URI` | mem://… | NATS workqueue for fan-out jobs |
| `OPPORTUNITY_FANOUT_QUEUE_NAME` | subject | Subject / register ref |
| `CANDIDATE_EMBEDDING_QUEUE_URI` | — | Path C index consumer |
| `MATCHING_CANDIDATE_CHANGE_ENABLED` | `true` | Path C consumer on/off (index-only when on) |
| `MATCHING_MIN_SCORE` | **`0.70`** | Quality floor for new index rows + invoke |
| `MATCHING_INVOKE_LIMIT_FREE` | **`1`** | Free daily `user_refresh` budget |
| `MATCHING_INVOKE_LIMIT_STARTER` | **`30`** | Starter daily invoke ceiling |
| `MATCHING_INVOKE_LIMIT_MANAGED` | **`100`** | Managed daily invoke ceiling |
| `DIGEST_DEFAULT_CADENCE` | `auto` | Request mode: `auto` / `twice_daily` / `daily` / `weekly` |
| `DIGEST_WEEKLY_WEEKDAY` | `monday` | Under auto, weekly users fire this weekday |
| `DIGEST_TIMEZONE` | `UTC` | Local zone for weekday + twice_daily windows |
| `DIGEST_TWICE_DAILY_MORNING_START` | `8` | twice_daily morning window start (local hour) |
| `DIGEST_TWICE_DAILY_MORNING_END` | `10` | morning end (exclusive) |
| `DIGEST_TWICE_DAILY_EVENING_START` | `17` | evening window start |
| `DIGEST_TWICE_DAILY_EVENING_END` | `19` | evening end (exclusive) |
| `PUBLIC_SITE_URL` | — | Links in notification payloads |
| `PLANS_URL` | production pricing URL | Free weekly-jobs digest CTA |

### Worker

| Env | Purpose |
|-----|---------|
| `WORKER_EMBED_QUEUE_URL` | Opportunity embed queue |
| `MATCHING_FANOUT_QUEUE_URL` | Publish `OpportunityFanOutV1` after embed (only useful if Path A is on) |

If `MATCHING_FANOUT_QUEUE_URL` is unset, embeds still work but Path A is not
fed. Path A is **off by default** — production invoke-only setups do not need
live fan-out. To re-enable Path A, set **both** worker `MATCHING_FANOUT_QUEUE_URL`
and matching `OPPORTUNITY_FANOUT_QUEUE_URI` to the same workqueue **and**
`MATCHING_FANOUT_ENABLED=true`.

Path A and Path C flags are **independent** (`MATCHING_FANOUT_ENABLED` vs
`MATCHING_CANDIDATE_CHANGE_ENABLED`). Path C index updates do not generate
matches.

## Reliability notes

- Fan-out stream uses **workqueue** retention so brief matching restarts do not
  drop messages (unlike interest retention) — relevant only when Path A is on.
- Fan-out `ack_wait=300s`, `max_ack_pending=4` — bounds concurrent reverse-KNN.
- Publish failure after embed is non-fatal; user refresh / digest recover via
  `MatchInvoke`.
- Upsert is score-monotonic and terminal-safe (dismissed/applied preserved).
- Digest receipts prevent re-sending the same match IDs in later digests.
- Nil notification client degrades to logged skip (matching still boots).
