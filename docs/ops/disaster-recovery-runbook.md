# Disaster Recovery Runbook

**Owner:** Platform / Peter Bwire
**Audience:** On-call operator responding to a production incident
**Scope:** stawi.opportunities data-plane recovery — Manticore, Valkey, Postgres, R2, Iceberg, full cluster

> This document combines and extends `docs/ops/runbook-manticore-rebuild.md` and
> `docs/ops/runbook-kv-rebuild.md` with additional DR scenarios.

---

## Quick reference — RTO / RPO table

| Scenario | Detection signal | RTO estimate | RPO (data loss) |
|----------|-----------------|--------------|-----------------|
| Manticore index wiped | Search returns 503 / 0 results | 60–120 min | 0 (rebuilds from R2) |
| Valkey KV lost | Duplicate canonicals; dedup:miss spike | 5–15 min | 0 (rebuilds from R2) |
| Postgres catalog loss | Writer + materializer crash on startup | 30–90 min | Events since last WAL backup |
| R2 log bucket deleted | Writer flush errors; Iceberg scan fails | 1–4 hours | All Parquet data |
| R2 content bucket deleted | /api/v2/jobs/<slug> returns 404 | Hours–days | All canonical JSON |
| Full cluster loss | Everything down | 3–6 hours | Same as above |
| Iceberg data corruption | Wrong query results; snapshot errors | 30 min | 0 (time travel) |

---

## Scenario 1 — Manticore index wiped

**Symptoms:**
- `GET /api/v2/search?q=engineer` returns 503 or empty hits
- `kubectl logs -l app=opportunities-materializer` shows `table not found` errors
- `SELECT COUNT(*) FROM idx_opportunities_rt` returns 0 or error

**Recovery:**

```bash
# 1. Confirm R2 event-log is intact
aws s3 ls s3://opportunities-log/canonicals_current/ | wc -l
# Expect ≥ 200 files

# 2. Drop and re-create the index
kubectl -n opportunities exec -i svc/manticore -- \
    /usr/bin/mysql -h127.0.0.1 -P9306 -e "DROP TABLE IF EXISTS idx_opportunities_rt;"

./scripts/bootstrap/create-manticore-schema.sh

# 3. Reset materializer consumer position so it replays from the beginning
# Frame/NATS: delete or reset the durable consumer so materializer replays all events
kubectl -n opportunities exec -it deploy/opportunities-materializer -- \
    nats consumer rm svc_opportunities_events materializer-events --force 2>/dev/null || true

# 4. Restart materializer
kubectl rollout restart deploy/opportunities-materializer -n opportunities

# 5. Monitor upsert rate
kubectl logs -f -l app=opportunities-materializer -n opportunities | grep "manticore upsert"
# Expect ≥ 500 upserts/min during replay

# 6. Poll row count
watch -n 30 'kubectl -n opportunities exec -i svc/manticore -- \
    /usr/bin/mysql -h127.0.0.1 -P9306 -e "SELECT COUNT(*) FROM idx_opportunities_rt;"'
```

**Estimated recovery time:** 60–120 minutes for 1M rows at ~500 upserts/s.

**Success signal:** `/api/v2/search?q=engineer` returns ≥ 10 hits.

---

## Scenario 2 — Valkey KV lost

**Symptoms:**
- Writer logs flooded with `dedup:miss` at high rate
- Duplicate canonical IDs appearing in search results
- `redis-cli -u $VALKEY_URL PING` returns `Connection refused` or times out

**Recovery:**

```bash
# 1. Confirm Valkey is reachable
redis-cli -u "${VALKEY_URL}" PING   # expect: PONG

# 2. Trigger KV rebuild via worker admin endpoint
# The worker reads cluster snapshots from R2 and repopulates Valkey
curl -XPOST "${WORKER_URL}/_admin/kv/rebuild"
# Response: {"rows":12345,"cluster_keys_set":12345,"files_scanned":48}

# 3. Verify
redis-cli -u "${VALKEY_URL}" KEYS "cluster:*" | wc -l
# Should match approximately the number of canonical clusters
```

**Scope:** The rebuild repopulates `cluster:{cluster_id}` keys only.
`dedup:{hard_key}` keys rebuild lazily as new variants flow through the pipeline.
Compaction re-merges any duplicate clusters via hard_key overlap.

**Estimated recovery time:** 5–15 minutes.

**Success signal:** Writer logs stop emitting `dedup:miss` at high rate;
canonical upsert rate returns to normal within 10 minutes.

---

## Scenario 3 — Total Postgres loss (catalog + sources)

**Symptoms:**
- Writer fails on startup: `failed to load Iceberg catalog`
- Crawler fails on startup: `database connection refused`
- All `ExternalSecret` objects for `db-credentials-opportunities` show `SecretSyncedError`

**Detection commands:**
```bash
kubectl get pods -n opportunities | grep -v Running
kubectl describe externalsecret db-credentials-opportunities -n opportunities
kubectl logs -l app=opportunities-writer -n opportunities | grep "catalog\|database" | tail -20
```

**Recovery (using CNPG WAL backup on R2):**

```bash
# 1. Check CNPG cluster status
kubectl get cluster -n datastore

# 2. Trigger point-in-time recovery via CNPG backup
# Edit the CNPG cluster manifest to set bootstrap.recovery.backup.name
# and set a target time (use the last known-good checkpoint):
kubectl edit cluster opportunities-db -n datastore
# Set: spec.bootstrap.recovery.source: <backup-name>
#      spec.bootstrap.recovery.recoveryTarget.targetTime: "2026-04-22T10:00:00Z"

# 3. Wait for CNPG to restore and promote the primary
kubectl get cluster opportunities-db -n datastore -w

# 4. Re-run Iceberg catalog migration if tables are missing
psql "${DATABASE_URL}" -f db/migrations/0004_iceberg_catalog.sql

# 5. Restart all opportunities services
kubectl rollout restart deployment -n opportunities
```

**Data loss tolerance:** Events since the last WAL backup segment (typically < 5 min
with CNPG continuous archiving to R2). Parquet files in R2 are unaffected.

**Estimated recovery time:** 30–90 minutes depending on WAL archive size.

---

## Scenario 4 — Total Valkey loss (see Scenario 2)

Covered in Scenario 2. Valkey is ephemeral — all data rebuilds from R2.

---

## Scenario 5 — R2 log bucket deleted (opportunities-log)

This is the most severe recoverable scenario. All Parquet event-log files are stored
here, as is the Iceberg metadata tree.

**Symptoms:**
- Writer flush errors: `NoSuchBucket` or `AccessDenied`
- Iceberg catalog returns `table metadata not found`
- `aws s3 ls s3://opportunities-log/` returns `NoSuchBucket`

**Recovery:**

```bash
# 1. Re-create the R2 bucket (via Cloudflare dashboard or API)
# Bucket name must match: opportunities-log
# Enable versioning if not already enabled

# 2. Re-seed Vault credentials if the R2 access key was also rotated
./scripts/bootstrap/seed-vault.sh

# 3. Re-bootstrap Iceberg (namespaces + tables — metadata only)
./scripts/bootstrap/create-iceberg.sh

# 4. Re-bootstrap Manticore (index is also empty now)
./scripts/bootstrap/create-manticore-schema.sh

# 5. Reset NATS consumer positions to replay from beginning
# This causes the writer to re-process all events from NATS retention window
# (default: 24h via stream_max_age=86400000000000)
# Events older than 24h are NOT in NATS — they are permanently lost from the log bucket

# 6. Restart all services
kubectl rollout restart deployment -n opportunities
```

**Data loss:** All Parquet files written before the bucket was deleted.
NATS retains 24 hours of events — the writer will replay those.
Events older than 24 hours are permanently lost unless a separate backup exists.

**Mitigation (prevent future loss):**
- Enable Cloudflare R2 Object Lock or versioning on `opportunities-log`
- Configure cross-bucket replication to a second R2 bucket as a backup
- Consider periodic Iceberg snapshot exports to a separate storage account

**Estimated recovery time:** 1–4 hours for re-bootstrap; 24h+ to rebuild Parquet
history from NATS replay window.

---

## Scenario 6 — R2 content bucket deleted (opportunities-content)

The content bucket stores canonical JSON files at `s3://opportunities-content/jobs/<slug>.json`.
These are the source of truth for `/api/v2/jobs/<slug>` detail pages.

**Symptoms:**
- `/api/v2/jobs/<slug>` returns 404 for all slugs
- `aws s3 ls s3://opportunities-content/jobs/` returns `NoSuchBucket`

**Recovery:**

```bash
# 1. Re-create the R2 content bucket
# 2. Re-seed R2 credentials if rotated
./scripts/bootstrap/seed-vault.sh

# 3. Trigger worker to re-write all canonical JSON files
# The worker reads from the Iceberg canonicals_upserted table and writes to R2
curl -XPOST "${WORKER_URL}/_admin/backfill-content"
# (If this endpoint doesn't exist, trigger via a manual Trustage scheduler-tick)
```

**Estimated recovery time:** Hours to days depending on canonical count and worker
throughput. Manticore search continues to work during recovery (the index is intact).

---

## Scenario 7 — Full cluster loss (greenfield rebuild from crawl only)

**Symptoms:** All nodes gone; `kubectl get nodes` returns empty or connection refused.

**Recovery:**

```bash
# 1. Provision a new cluster (Talos/k8s)
#    See: /home/j/code/antinvestor/deployments/setup_cluster_talos.yml

# 2. Bootstrap Flux
#    See: /home/j/code/antinvestor/deployments/setup_cluster_fluxcd.yml

# 3. Wait for cluster infrastructure to stabilize
kubectl get nodes

# 4. Run full bootstrap
./scripts/bootstrap/bootstrap.sh

# 5. Apply deployment manifests
kubectl apply -k manifests/namespaces/opportunities/

# 6. Wait for fill checkpoint (same as first-deploy-runbook.md §6)
```

**Estimated recovery time:** 3–6 hours (cluster provisioning + bootstrap + fill).

**Data loss:** Equivalent to R2 log bucket scenario — NATS history is gone.
The crawlers will re-discover sources and re-ingest data within 24–48 hours.

---

## Scenario 8 — Iceberg data corruption (partial, revertible)

**Symptoms:**
- Writer returns `EqualityDelete conflict` or `ManifestEntryConflict`
- Query results show duplicate or stale rows
- `pyiceberg table history` shows an unexpected snapshot

**Recovery via time travel:**

```python
from pyiceberg.catalog import load_catalog
from pyiceberg.expressions import LessThanOrEqual
import os, datetime

cat = load_catalog("stawi", **{
    "type": "sql",
    "uri": os.environ["ICEBERG_CATALOG_URI"],
    "s3.endpoint": os.environ["R2_ENDPOINT"],
    "s3.access-key-id": os.environ["R2_ACCESS_KEY_ID"],
    "s3.secret-access-key": os.environ["R2_SECRET_ACCESS_KEY"],
    "s3.region": "auto",
    "warehouse": "s3://opportunities-log/iceberg",
})

table = cat.load_table("jobs.variants")

# List recent snapshots
for snap in table.history():
    print(snap.snapshot_id, snap.timestamp_ms, snap.operation)

# Roll back to a specific snapshot ID
target_snapshot_id = 1234567890  # replace with the last known-good snapshot
table.manage_snapshots().rollback_to_snapshot(target_snapshot_id).commit()
print("Rolled back to snapshot", target_snapshot_id)
```

**Time travel read (non-destructive verify before rollback):**
```python
# Read the table as it was at a specific time
from datetime import datetime, timezone
as_of = datetime(2026, 4, 21, 12, 0, 0, tzinfo=timezone.utc)
scan = table.scan(snapshot_id=table.snapshot_by_id(as_of).snapshot_id)
for batch in scan.to_arrow_batch_reader():
    print(batch.schema)
    break
```

**Estimated recovery time:** 30 minutes (identify corrupt snapshot + rollback).
**Data loss:** Events written after the rolled-back snapshot are lost.
