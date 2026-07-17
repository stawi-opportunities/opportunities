# Matching pipeline (production)

## Paths

| Path | Trigger | Action | User notification |
|------|---------|--------|-------------------|
| **A FanOut** | Opportunity embed succeeds | Reverse-KNN → score → `candidate_matches` | Always via **service-notification**; `delivery=immediate` only if `match_alerts=true`, else `delivery=digest` |
| **C Gap-fill** | CV/candidate embedding | Reverse-KNN for one candidate | Collect only (no per-run notify; digests cover summaries) |
| **Preference** | Preferences updated | KNN rematch (blended score) | Always via service-notification; immediate only if `match_alerts=true` |
| **HTTP match** | `GET /candidates/match` | On-demand KNN | Always via service-notification; immediate only if `match_alerts=true` |
| **Digest** | Trustage cron (configurable) | Gap-fill + summary | Always via service-notification (`matches_digest` / `weekly_jobs_digest`) |
| **CV stale** | Trustage cron | Nudge for old CV | Always via service-notification (`cv_stale_nudge`) |

**Rule:** matching never sends email/SMS itself. All candidate-facing messages
queue through `NotificationService.Send` (`pkg/notify`). Domain events on the
matching bus remain for analytics/bridges only.

Default UX: **collect matches always; digest delivery**. Real-time every-match
send is opt-in via Settings → “Notify on every match” (`match_alerts`).

## Scoring

All paths use the same cosine term (`CosineFromPGDistance` / blend weights):

- Cosine 0.60, Skills 0.15, Geo 0.15, Salary 0.10, Stale −0.10  
- Floor: `MATCHING_MIN_SCORE` (default **0.45**)  
- Plan caps: daily/weekly on index; overflow rows hidden from feed defaults  

## Deploy env

### Matching

| Env | Purpose |
|-----|---------|
| `NOTIFICATION_SERVICE_URI` | service-notification base URL (required for user mail) |
| `MATCHING_FANOUT_ENABLED` | Path A consumer (default true) |
| `OPPORTUNITY_FANOUT_QUEUE_URI` | NATS workqueue for fan-out jobs |
| `OPPORTUNITY_FANOUT_QUEUE_NAME` | Subject / register ref |
| `CANDIDATE_EMBEDDING_QUEUE_URI` | Path C |
| `MATCHING_MIN_SCORE` | Quality floor |
| `DIGEST_*` | Digest cadence filtering |
| `PUBLIC_SITE_URL` | Links in notification payloads |

### Worker

| Env | Purpose |
|-----|---------|
| `WORKER_EMBED_QUEUE_URL` | Opportunity embed queue |
| `MATCHING_FANOUT_QUEUE_URL` | Publish `OpportunityFanOutV1` after embed |

If `MATCHING_FANOUT_QUEUE_URL` is unset, embeds still work but Path A is not
fed (digests/Path C still collect matches).

## Notification templates

Registered in service-notification (payload built by `pkg/notify`):

| Template | When |
|----------|------|
| `matches_ready` | Path A / preference / HTTP match |
| `matches_digest` | Paid subscriber digest cron |
| `weekly_jobs_digest` | Unpaid re-engagement digest |
| `cv_stale_nudge` | Stale CV cron |

Recipient is the candidate `profile_id` (falls back to candidate id).

## Reliability notes

- Fan-out stream uses **workqueue** retention so brief matching restarts do not
  drop messages (unlike interest retention).
- Fan-out `ack_wait=300s`, `max_ack_pending=4` — bounds concurrent reverse-KNN.
- Publish failure after embed is non-fatal; gap-fill/digest recover.
- Upsert is score-monotonic and terminal-safe (dismissed/applied preserved).
- Nil notification client degrades to logged no-ops (matching still boots).
