# Data storage map

How data is stored, why each shape exists, and whether it is fit for production.

## Principles

| Kind of data | Store as | Why |
|--------------|----------|-----|
| Current truth (mutable) | Ordinary PostgreSQL table | Updates, leases, soft hide |
| Time-ordered audit / telemetry | TimescaleDB hypertable (append-only) | Partition, compress, retain |
| Large opaque blobs | Cloudflare R2 | Cheap, content-addressable or keyed |
| Hot path control (debounce, 24h counters) | Valkey (optional) | Low latency, TTL |
| Control / wake-up signals | NATS JetStream | Not source of truth for work |
| Config as data | Seeds / YAML → PG or R2 | No redeploy for coverage |

---

## System diagram

```text
                    ┌─────────────────────────────────────────┐
                    │  Disk seeds + YAML  │  R2 definitions   │
                    └──────────┬──────────┴─────────┬─────────┘
                               │ boot / admin        │ loader
                               ▼                     ▼
┌──────────────────────────────────────────────────────────────────┐
│                     PostgreSQL + Timescale + pgvector              │
│                                                                  │
│  CRAWL          sources, source_recipes, crawl_runs, crawl_jobs  │
│                 url_frontier, host_state                         │
│  INGEST         job_ingest_queue ──► job_ingest_events (HT)      │
│  SERVE          opportunity_identities, opportunities,           │
│                 opportunity_sources, companies, flags            │
│  CANDIDATE      candidate_profiles, preferences, match_rules     │
│  MATCH          candidate_match_indexes (HNSW), candidate_matches│
│                 candidate_match_events (HT), match_run_events    │
│                 engagement_events (HT), saved_jobs               │
│  APPLY          applications, notes, attachments, reminders      │
│                 application_events (HT), idempotency_keys        │
│  BILLING        candidate_checkouts (+ profile subscription cols)│
└──────────────────────────────────────────────────────────────────┘
         ▲ claim/lease              │ search / KNN
         │                          ▼
   apps/worker                 apps/api, apps/matching
         ▲
   NATS wake-ups (crawl.requests, url.enqueued, …)
```

---

## 1. Crawl & control plane

| Store | Type | Role | Robustness | Efficiency |
|-------|------|------|------------|------------|
| **`sources`** | OLTP | Registry: type, URL, status, health, recipe JSON, frontier flag | Strong unique `(type, base_url)`; status gates schedules | Hot row; recipe dual-stored with `source_recipes` (TX-synced) |
| **`source_recipes`** | OLTP | Recipe version history | Partial unique one `active` per source | Append history; only active used on hot path |
| **`crawl_runs`** | OLTP | Resumable cursor + lease | Partial unique one active run/source | Single-flight prevents pile-up |
| **`crawl_jobs`** | Hypertable 1d | Execution ledger | Idempotency `(key, scheduled_at)`; **90d retain**, compress 14d | Time-partitioned audit — correct |
| **`url_frontier`** | OLTP queue | Detail URLs under politeness | UK `canonical_url_hash`; SKIP LOCKED | Per-host fairness via `host_state` |
| **`host_state`** | OLTP | Per-host rate window | PK host | Tiny |

**Verdict:** Solid. Queue + lease + hypertable history is the right split.

**Watch:** `crawl_signals` MV must be refreshed (function exists; ensure Trustage/cron calls `refresh_crawl_signals`).

---

## 2. Ingest pipeline

| Store | Type | Role | Robustness | Efficiency |
|-------|------|------|------------|------------|
| **`job_ingest_queue`** | Mutable queue | Durable work after extract | UK `idempotency_key` + `variant_id`; claim + lease indexes; cleanup job 30d/90d | Pending has **no TTL** (correct for durability); capacity gates stop crawl |
| **`job_ingest_events`** | Hypertable 1d | Append-only audit | Append-only triggers; **90d retain**, compress 7d | Good |

**Idempotency:** `crawlJobID:hardKey` or `frontier:urlID:hardKey` — safe redelivery.

**Verdict:** Most robust durable handoff pattern in the stack. Prefer this over putting job payloads only on NATS.

---

## 3. Canonical opportunities (serving)

| Store | Type | Role | Robustness | Efficiency |
|-------|------|------|------------|------------|
| **`opportunity_identities`** | OLTP | `hard_key` → `canonical_id` | PK hard_key; UK canonical | O(1) merge |
| **`opportunities`** | OLTP + vector | Serving row | Apply URL CHECK; partial facet indexes; **HNSW on embedding** (capability SQL) | HNSW required for KNN scale |
| **`opportunity_sources`** | OLTP | Lineage / multi-source | PK `(canonical, source, external)`; active flag | Full-crawl recon hides stale |
| **`companies`** | OLTP | Issuer rollup | UK slug | Lightweight |
| **`opportunity_flags`** | OLTP | User reports | UK (slug, reporter) | Low volume |

**Identity:** `BuildHardKey(company, title, location, postingID)` with postingID preferring board external_id + apply-URL identity.

**Verdict:** Correct “current state + lineage” model.  
**Growth:** No hard delete of hidden jobs — intentional soft retention; rely on hide + periodic expire admin. At multi-million rows, plan archival of long-hidden opportunities.

---

## 4. Candidates

| Store | Type | Role | Robustness | Efficiency |
|-------|------|------|------------|------------|
| **`candidate_profiles`** | OLTP | Profile, plan, CV pointers, report cache | Soft-delete BaseModel | Wide row — acceptable for 1:1 profile |
| **R2 archive** | Blob | Raw CV bytes | Content-hash keys | Good; keep hashes on profile for change detection |
| **`candidate_applications`** | OLTP | **Legacy** still AutoMigrated | Dual with `applications` | **Debt — stop writing; drop after cutover** |

**Verdict:** Profile + R2 pointer is right. Retire legacy `candidate_applications` when safe.

---

## 5. Matching

| Store | Type | Role | Robustness | Efficiency |
|-------|------|------|------------|------------|
| **`candidate_match_indexes`** | OLTP + **HNSW** | Fan-out vectors + caps | HNSW WHERE enabled | Correct ANN for fan-out |
| **`candidate_matches`** | OLTP | Current pairs | UK pair; score/status indexes | Score-monotonic upsert protects terminals |
| **`candidate_preferences` / `match_rules`** | OLTP | Prefs / rules JSON | PK candidate | Small |
| **`candidate_match_events`** | Hypertable | Ledger | 365d retain, compress, CAGG daily | Daily cap uses CAGG + raw tail — good |
| **`match_run_events`** | Hypertable | Run telemetry | **90d retain + compress** | Ops only |
| **`engagement_events`** | Hypertable | Beacons | 180d retain, CAGG hourly | Good |
| **`candidate_saved_jobs`** | OLTP | Stars | Composite PK | Fine |

**Valkey debounce** (`matching:debounce:{id}`): multi-pod; SETNX preferred over Exists+Set when tightening races.

**Verdict:** Strong. Index vs events split is textbook.

---

## 6. Applications

| Store | Type | Role | Robustness | Efficiency |
|-------|------|------|------------|------------|
| **`applications`** | OLTP | Tracker state machine | UK pair | Correct |
| **notes / reminders / attachments** | OLTP | Subresources | Soft-delete notes/attachments | Fine |
| **`application_events`** | Hypertable | Audit | Compress; **no retention** (legal/audit) | OK if intentional |
| **`idempotency_keys`** | OLTP | HTTP replay | TTL columns; need purge job scheduled | Schedule Purge if not already |
| **R2 attachments** | Blob | Files | Prefixed keys; soft-delete may orphan blobs | Add lifecycle or GC |

**Verdict:** Good CRM shape. GC for R2 soft-deletes is the main gap.

---

## 7. Billing

| Store | Type | Role |
|-------|------|------|
| **`candidate_checkouts`** | OLTP | Prompt ledger + status |
| **`candidate_profiles`** fields | OLTP | `subscription`, `plan_id`, `subscription_id` |

External payment/billing services are SoT for rails; local row is product entitlement cache. Reconciler Trustage job closes webhook gaps.

**Verdict:** Appropriate for delegated payments.

---

## 8. Non-Postgres

| System | What | SoT? | Notes |
|--------|------|------|-------|
| **R2** | CVs, attachments, definition YAML | Blobs + config | Definitions cached in-process |
| **NATS** | Crawl control, CV queues, notify envelopes | **No** for job rows | Postgres queues are SoT for ingest/frontier |
| **Valkey** | Debounce, view/apply counters | No | View totals keys lack TTL → unbounded slug cardinality |
| **Trustage** | Cron definitions | External | JSON under `definitions/trustage/` |
| **OpenObserve / OTel** | Analytics / metrics | External | |

---

## 9. Efficiency scorecard

| Layer | Score | Comment |
|-------|-------|---------|
| Ingest queue + worker TX | **Excellent** | Lease, SKIP LOCKED, idempotent, single TX merge |
| Event hypertables + retention | **Excellent** | Clear policies; append-only guards |
| Identity merge | **Good** | hard_key; apply-URL identity reduces collisions |
| Opportunity serving indexes | **Good** | Facet partials + HNSW for embeddings |
| Candidate match index | **Excellent** | HNSW + caps denormalized |
| Recipe dual-write | **OK** | TX-protected; document as intentional |
| Unbounded serving growth | **Acceptable** | Monitor; plan archive for long-hidden |
| Legacy dual applications tables | **Poor** | Remove when unused |
| Valkey total counters | **OK at small scale** | Prefer TTLd or PG aggregates later |
| Attachment blob GC | **Gap** | Soft-delete orphans |

---

## 10. What we hardened in capability SQL

1. **`opportunities` HNSW** on non-null embeddings (active rows) — required for efficient reverse KNN / search.  
2. **`match_run_events` retention 90d + compression** — ops telemetry should not grow forever.

Both migrations are idempotent (`IF NOT EXISTS` / `if_not_exists`).

---

## 11. Recommended next storage work (priority)

| P | Item | Why |
|---|------|-----|
| P1 | Confirm `refresh_crawl_signals` on a schedule | Adaptive freshness / admin health |
| P1 | Drop or stop migrating `candidate_applications` | Dual schema risk |
| P2 | R2 lifecycle for soft-deleted application attachments | Cost + privacy |
| P2 | Valkey: TTL or migrate view totals to PG CAGG | Unbounded keys |
| P2 | Archive/hide-delete path for opportunities not seen in N months | Disk |
| P3 | Debounce SETNX | Multi-pod races |

---

## 12. Mental model

1. **Control** — sources + Trustage + NATS wake-ups  
2. **Work** — `crawl_runs` / `job_ingest_queue` / `url_frontier` (leased, durable)  
3. **Truth** — `opportunities` + lineage + identities  
4. **People** — `candidate_profiles` + R2 CVs  
5. **Relevance** — vector indexes + `candidate_matches` + event logs  
6. **CRM** — `applications` + attachment blobs  
7. **Money** — checkouts + profile subscription fields  

Each level uses the right durability class. The main efficiency wins left are **ANN on jobs** (done in migration), **telemetry retention** (done for match runs), and **janitors** for legacy tables / blobs / counters.
