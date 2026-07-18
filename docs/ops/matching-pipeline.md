# Matching pipeline (production)

## Paths

| Path | Trigger | Action | User notification |
|------|---------|--------|-------------------|
| **A FanOut** | Opportunity embed succeeds | Reverse-KNN on **conversation-grounded persona** vectors → score → caps → `candidate_matches` | `NotificationService.Send` when `match_alerts=true` |
| **C Gap-fill** | Persona/CV embedding event | Reverse-KNN for one candidate | Collect only (digests cover summaries) |

### Matching persona (candidate side)

Every chat turn rebuilds a **Matching persona v1** document:

1. **Intent** — rolling conversation digest (user turns, not full transcript)
2. **Preferences** — structured fields (role, markets, salary, types)
3. **Qualifications** — CV / skills

Stored on `candidate_placement_profiles` (`summary_text`, `conversation_digest`).  
Embedded into `candidate_match_indexes` with `rerank_text` for Path A stage-2.

**Dual-writer rule:** persona embeds (`source=persona`) own the index. Thin async CV-field embeds (`source=cv_fields`) cannot overwrite when `rerank_text` is set.

**Caps on Path A:** daily (events) + weekly remaining (non-overflow rows) — free proof and Starter stay honest under live job ingest.
| **Preference** | Preferences updated | KNN rematch (blended score) | `Send` when `match_alerts=true` |
| **HTTP match** | `GET /candidates/match` | On-demand KNN | `Send` when `match_alerts=true` |
| **Digest** | Trustage cron | Gap-fill + summary | Always `Send` (`matches.digest` / `weekly_jobs.digest`) |
| **CV stale** | Trustage cron | Nudge for old CV | Always `Send` (`cv.stale_nudge`) |

**Rule:** matching never sends email/SMS itself. Delivery uses the same
constructs as **service-profile**:

1. `connection.NewServiceClient` → `notificationv1connect.NotificationServiceClient`
2. Build `notificationv1.Notification` with `Template`, `Payload` (`structpb`),
   `Recipient` (`ContactLink` with `ProfileType` + `ProfileId`), `OutBound`,
   `AutoRelease`
3. `NotificationService.Send` and drain the stream (`pkg/notify.Send`)

Domain events on the matching bus remain for analytics/bridges only.

Default UX: **collect matches always; digest on schedule**. Real-time
every-match send is opt-in via Settings → “Notify on every match”
(`match_alerts`).

## Scoring

All paths use the same cosine term (`CosineFromPGDistance` / blend weights):

- Cosine 0.60, Skills 0.15, Geo 0.15, Salary 0.10, Stale −0.10  
- Floor: `MATCHING_MIN_SCORE` (default **0.45**)  
- Plan caps: daily/weekly on index; overflow rows hidden from feed defaults  

## Deploy env

### Matching

| Env | Purpose |
|-----|---------|
| `NOTIFICATION_SERVICE_URI` | service-notification base URL |
| `NOTIFICATION_SERVICE_WORKLOAD_API_TARGET_PATH` | SPIFFE path (profile-style; default `/ns/notifications/sa/service-notification`) |
| `MESSAGE_TEMPLATE_MATCHES_READY` | default `template.opportunities.matches.ready` |
| `MESSAGE_TEMPLATE_MATCHES_DIGEST` | default `template.opportunities.matches.digest` |
| `MESSAGE_TEMPLATE_WEEKLY_JOBS_DIGEST` | default `template.opportunities.weekly_jobs.digest` |
| `MESSAGE_TEMPLATE_CV_STALE_NUDGE` | default `template.opportunities.cv.stale_nudge` |
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
fed (digests/Path C still collect matches). **Production must set both** worker
`MATCHING_FANOUT_QUEUE_URL` and matching `OPPORTUNITY_FANOUT_QUEUE_URI` to the
same workqueue for Path A to be live.

## Reliability notes

- Fan-out stream uses **workqueue** retention so brief matching restarts do not
  drop messages (unlike interest retention).
- Fan-out `ack_wait=300s`, `max_ack_pending=4` — bounds concurrent reverse-KNN.
- Publish failure after embed is non-fatal; gap-fill/digest recover.
- Upsert is score-monotonic and terminal-safe (dismissed/applied preserved).
- Nil notification client degrades to logged skip (matching still boots).
