# Matching pipeline (production)

## Paths

| Path | Trigger | Action | User notification |
|------|---------|--------|-------------------|
| **A FanOut** | Opportunity embed succeeds | Reverse-KNN → score → `candidate_matches` | Only if `match_alerts=true` |
| **C Gap-fill** | CV/candidate embedding | Reverse-KNN for one candidate | Only if `match_alerts=true` |
| **Preference** | Preferences updated | KNN rematch (blended score) | Only if `match_alerts=true` |
| **Digest** | Trustage cron (configurable) | Gap-fill + emit summary | If `weekly_summary` + digest cadence |

Default: **collect matches always; email digests on schedule**. Real-time
every-match email is opt-in via Settings → “Notify on every match”.

## Scoring

All paths use the same cosine term (`CosineFromPGDistance` / blend weights):

- Cosine 0.60, Skills 0.15, Geo 0.15, Salary 0.10, Stale −0.10  
- Floor: `MATCHING_MIN_SCORE` (default **0.45**)  
- Plan caps: daily/weekly on index; overflow rows hidden from feed defaults  

## Deploy env

### Matching

| Env | Purpose |
|-----|---------|
| `MATCHING_FANOUT_ENABLED` | Path A consumer (default true) |
| `OPPORTUNITY_FANOUT_QUEUE_URI` | NATS workqueue for fan-out jobs |
| `OPPORTUNITY_FANOUT_QUEUE_NAME` | Subject / register ref |
| `CANDIDATE_EMBEDDING_QUEUE_URI` | Path C |
| `MATCHING_MIN_SCORE` | Quality floor |
| `DIGEST_*` | Digest cadence filtering |

### Worker

| Env | Purpose |
|-----|---------|
| `WORKER_EMBED_QUEUE_URL` | Opportunity embed queue |
| `MATCHING_FANOUT_QUEUE_URL` | Publish `OpportunityFanOutV1` after embed |

If `MATCHING_FANOUT_QUEUE_URL` is unset, embeds still work but Path A is not
fed (digests/Path C still collect matches).

## Reliability notes

- Fan-out stream uses **workqueue** retention so brief matching restarts do not
  drop messages (unlike interest retention).
- Fan-out `ack_wait=300s`, `max_ack_pending=4` — bounds concurrent reverse-KNN.
- Publish failure after embed is non-fatal; gap-fill/digest recover.
- Upsert is score-monotonic and terminal-safe (dismissed/applied preserved).
