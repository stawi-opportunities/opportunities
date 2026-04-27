# v8.0.0 Opportunity Generification Runbook

**Date:** 2026-04-26
**Scope:** Cutover from job-only platform (v7.x) to polymorphic-opportunity platform (v8.0.0). One-time greenfield schema cut — Iceberg tables, Manticore index, NATS stream, and ConfigMap mounts all change.
**Audience:** Operators applying `feat/opportunity-generification` to a running cluster.

---

## What changes

- **Iceberg `opportunities.variants`, `embeddings`, `published`** — schemas rewritten with `kind` discriminator + `attributes` JSON column. Old job-shaped columns dropped.
- **Iceberg `opportunities.variants_rejected`** — new dead-letter table.
- **Manticore `idx_opportunities_rt`** — schema rewritten with `kind`, universal location/monetary columns, sparse per-kind facet columns.
- **NATS JetStream** — stream renamed `svc_opportunities_candidates` → `svc_opportunities_matching`; subject `svc.opportunities.candidates.>` → `svc.opportunities.matching.>`; consumer `candidates-events` → `matching-events`.
- **ConfigMap `opportunity-kinds`** — new ConfigMap mounted on every workload at `/etc/opportunity-kinds`.
- **App rename** — `opportunities-candidates` → `opportunities-matching` (HelmRelease, image, ServiceAccount, OTEL service name).
- **Sources table** — gains `kinds TEXT[]` and `required_attributes_by_kind JSONB` columns (GORM AutoMigrate).
- **R2 path layout** — slugs land at `<URLPrefix>/<slug>.json`. The `job` kind keeps its `jobs/` prefix; new kinds get their own (`scholarships/`, `tenders/`, `deals/`, `funding/`).

This is a **greenfield cut**. No backfill of job-shaped data into the new schema. Existing job records in Iceberg/Manticore are dropped and the platform rebuilds from live crawls.

---

## Pre-cutover

- [ ] CI green on `feat/opportunity-generification`.
- [ ] All six v8.0.0 images pushed to GHCR:
  - `ghcr.io/stawi-opportunities/opportunities-api:v8.0.0`
  - `ghcr.io/stawi-opportunities/opportunities-crawler:v8.0.0`
  - `ghcr.io/stawi-opportunities/opportunities-materializer:v8.0.0`
  - `ghcr.io/stawi-opportunities/opportunities-matching:v8.0.0` (note: not `candidates`)
  - `ghcr.io/stawi-opportunities/opportunities-worker:v8.0.0`
  - `ghcr.io/stawi-opportunities/opportunities-writer:v8.0.0`
- [ ] Deployments-repo PR merged with the v8 manifests (ConfigMap, HelmRelease updates, NATS stream rename).
- [ ] Smoke environment available for the staging dry-run below.

---

## Step 1 — Stop ingest and drain

```bash
# Pause the crawl scheduler so no new VariantIngested events land
# during the schema cut.
kubectl -n opportunities scale deployment opportunities-crawler --replicas=0
kubectl -n opportunities scale deployment opportunities-worker --replicas=0
kubectl -n opportunities scale deployment opportunities-materializer --replicas=0
kubectl -n opportunities scale deployment opportunities-writer --replicas=0
kubectl -n opportunities scale deployment opportunities-api --replicas=0

# Wait for in-flight events to drain (NATS pending count → 0).
nats stream info svc_opportunities_candidates -s nats://core-queue.queue-system.svc:4222
# Repeat until "Messages: 0".
```

If you can't get pending count to zero (stuck consumer), advance the consumer past the pending offset — accept the loss, since the records will be re-crawled.

---

## Step 2 — Drop old NATS stream

```bash
nats stream rm svc_opportunities_candidates -s nats://core-queue.queue-system.svc:4222 -f
```

The new `svc_opportunities_matching` stream will be created by FluxCD reconciling the updated `crawler/queue_setup.yaml` once Step 6 runs.

---

## Step 3 — Drop old Iceberg tables

```bash
# Connect to the JDBC catalog (Postgres-backed)
psql "$ICEBERG_CATALOG_URI" <<'SQL'
-- Old job-shaped tables
DROP TABLE IF EXISTS iceberg.opportunities.variants CASCADE;
DROP TABLE IF EXISTS iceberg.opportunities.embeddings CASCADE;
DROP TABLE IF EXISTS iceberg.opportunities.published CASCADE;
SQL
```

If your catalog uses pyiceberg admin commands instead of raw SQL, the equivalent is:

```bash
python3 - <<'PY'
from pyiceberg.catalog import load_catalog
cat = load_catalog("opportunities")
for tbl in ("variants", "embeddings", "published"):
    try:
        cat.drop_table(("opportunities", tbl))
        print(f"dropped opportunities.{tbl}")
    except Exception as e:
        print(f"skip opportunities.{tbl}: {e}")
PY
```

---

## Step 4 — Register the Lakekeeper warehouse and recreate Iceberg tables

The opportunities platform now talks to the cluster's shared Lakekeeper REST
catalog at `http://lakekeeper-catalog.lakehouse.svc.cluster.local:8181/catalog`
instead of running its own SQL/Postgres catalog. Lakekeeper holds the
metadata DB and the storage credentials; apps no longer need a
catalog-specific Postgres DSN or R2 access keys to commit snapshots.

### 4a. Register the `product-opportunities` warehouse (one-time)

The warehouse must exist in Lakekeeper before any commit. Run this from a
debug pod inside the cluster (or `kubectl exec` on a pod that already has
egress to the lakehouse namespace).

```bash
# Verify Lakekeeper is reachable + see version-specific schema.
curl -fsS http://lakekeeper-catalog.lakehouse.svc.cluster.local:8181/management/v1/info

# (Optional) fetch the canonical OpenAPI schema for the management API.
# Lakekeeper 0.10.x exposes this under /management/v1/openapi.json or
# /management/v1/openapi — check whichever the deployed version serves.
curl -fsS http://lakekeeper-catalog.lakehouse.svc.cluster.local:8181/management/v1/openapi.json | jq .
```

Pull the R2 log credentials from Vault (`kv/data/r2-log-credentials-opportunities`)
into local shell variables, then POST the warehouse definition:

```bash
curl -fsS -X POST http://lakekeeper-catalog.lakehouse.svc.cluster.local:8181/management/v1/warehouse \
  -H "Content-Type: application/json" \
  -d "{
    \"warehouse-name\": \"product-opportunities\",
    \"project-id\": \"00000000-0000-0000-0000-000000000000\",
    \"storage-profile\": {
      \"type\": \"s3\",
      \"bucket\": \"opportunities-log\",
      \"endpoint\": \"https://${R2_ACCOUNT_ID}.r2.cloudflarestorage.com\",
      \"region\": \"auto\",
      \"key-prefix\": \"iceberg\",
      \"path-style-access\": true,
      \"flavor\": \"s3-compat\"
    },
    \"storage-credential\": {
      \"type\": \"s3\",
      \"credential-type\": \"access-key\",
      \"aws-access-key-id\": \"${R2_LOG_ACCESS_KEY_ID}\",
      \"aws-secret-access-key\": \"${R2_LOG_SECRET_ACCESS_KEY}\"
    }
  }"
```

Idempotency: POST returns 409 Conflict if the warehouse already exists —
that's fine on re-runs. The exact request shape may vary between
Lakekeeper versions; consult the OpenAPI document fetched above for the
deployed chart version (currently `lakekeeper-0.10.1`). If the deployed
version uses a slightly different field name, adapt accordingly.

Lakekeeper's `LAKEKEEPER__OPENID_PROVIDER_URI` is unset in the cluster
chart, so auth is disabled for v1 — no bearer token is required for
either the management API or the catalog API. When OIDC is later enabled,
each app reads `ICEBERG_CATALOG_TOKEN` (or the operator switches to the
client-credentials path via a small extension to `pkg/icebergclient`).

### 4b. Create the project namespaces + tables

```bash
python3 definitions/iceberg/create_tables.py
```

The script creates:
- `opportunities.variants` (new shape: kind discriminator + attributes JSON)
- `opportunities.variants_rejected` (new dead-letter table)
- `opportunities.embeddings` (new: kind + vector + embedded_at)
- `opportunities.published` (mirrors variants)
- `opportunities.crawl_page_completed`, `opportunities.sources_discovered` (unchanged)
- `candidates.*` (unchanged)

Verify via the REST catalog (no longer via psql — the catalog is HTTP):

```bash
curl -fsS \
  "http://lakekeeper-catalog.lakehouse.svc.cluster.local:8181/catalog/v1/namespaces" \
  -H "X-Iceberg-Access-Delegation: vended-credentials" \
  | jq

# Per-namespace table listing:
curl -fsS \
  "http://lakekeeper-catalog.lakehouse.svc.cluster.local:8181/catalog/v1/namespaces/opportunities/tables" \
  | jq
curl -fsS \
  "http://lakekeeper-catalog.lakehouse.svc.cluster.local:8181/catalog/v1/namespaces/candidates/tables" \
  | jq
```

Expected: 12 tables (5 opportunities + 6 candidates + 1 new opportunities.variants_rejected).

---

## Step 5 — Drop and recreate Manticore index

```bash
# DROP the old job-shaped index
curl -s "$MANTICORE_URL/sql?mode=raw" -d "query=DROP TABLE IF EXISTS idx_opportunities_rt"

# Apply the new DDL via the materializer's idempotent boot path:
# (See Step 6 — the materializer calls searchindex.Apply() at boot which
# creates the index with the new schema. No manual CREATE TABLE needed.)
```

Optionally pre-create via SQL if you want to inspect the schema before pods boot:

```bash
# Extract the DDL from the Go source:
grep -A 60 "idxOpportunitiesRTDDL" pkg/searchindex/schema.go | head -80
# Apply it:
curl -s "$MANTICORE_URL/sql?mode=raw" -d "query=$(<the DDL above>)"
```

Verify after creation:

```bash
curl -s "$MANTICORE_URL/sql?mode=raw" -d "query=DESCRIBE idx_opportunities_rt"
```

Expected columns include: `kind`, `title`, `description`, `issuing_entity`, `categories`, `country`, `region`, `city`, `lat`, `lon`, `remote`, `geo_scope`, `posted_at`, `deadline`, `amount_min`, `amount_max`, `currency`, `employment_type`, `seniority`, `field_of_study`, `degree_level`, `procurement_domain`, `funding_focus`, `discount_percent`, `embedding`.

---

## Step 6 — Apply the new manifests

```bash
# In the deployments repo, on main with the v8 commit landed:
git -C /home/j/code/antinvestor/deployments pull origin main
kubectl apply -k /home/j/code/antinvestor/deployments/manifests/namespaces/opportunities/
```

Watch FluxCD reconcile:

```bash
kubectl -n opportunities get helmreleases.helm.toolkit.fluxcd.io
kubectl -n opportunities get configmaps opportunity-kinds
kubectl -n queue-system get streams svc-opportunities-matching
```

Expected:
- ConfigMap `opportunity-kinds` mounted (5 keys: deal.yaml, funding.yaml, job.yaml, scholarship.yaml, tender.yaml).
- Stream `svc_opportunities_matching` created with subjects `svc.opportunities.matching.>`.
- All HelmReleases reach Ready.

---

## Step 7 — Bring services back online

```bash
# Scale up in dependency order: writer first (subscribes to events,
# persists to Iceberg), then materializer (subscribes + indexes to Manticore),
# then matching, api, worker, crawler.
kubectl -n opportunities scale deployment opportunities-writer --replicas=1
kubectl -n opportunities rollout status deployment opportunities-writer

kubectl -n opportunities scale deployment opportunities-materializer --replicas=1
kubectl -n opportunities rollout status deployment opportunities-materializer

kubectl -n opportunities scale deployment opportunities-matching --replicas=1
kubectl -n opportunities rollout status deployment opportunities-matching

kubectl -n opportunities scale deployment opportunities-api --replicas=1
kubectl -n opportunities rollout status deployment opportunities-api

kubectl -n opportunities scale deployment opportunities-worker --replicas=1
kubectl -n opportunities rollout status deployment opportunities-worker

kubectl -n opportunities scale deployment opportunities-crawler --replicas=1
kubectl -n opportunities rollout status deployment opportunities-crawler
```

If any pod crash-loops, check:

- ConfigMap mount: `kubectl -n opportunities exec deployment/opportunities-<app> -- ls /etc/opportunity-kinds` — should list 5 YAMLs.
- Registry load log: `kubectl -n opportunities logs deployment/opportunities-<app> | grep "opportunity registry: loaded"` — should show `kinds=[deal funding job scholarship tender]`.
- GORM AutoMigrate (crawler only): the new `kinds` and `required_attributes_by_kind` columns should appear on the `sources` table after the crawler boots.

---

## Step 8 — Smoke check

Trigger one crawl and verify a record appears at every stage:

```bash
# Pick an existing job source (e.g. RemoteOK)
SOURCE_ID=$(psql "$DATABASE_URL" -t -c "SELECT id FROM sources WHERE type='remoteok' LIMIT 1" | xargs)

# Trigger a crawl
kubectl -n opportunities exec deployment/opportunities-crawler -- \
    curl -s -X POST "http://localhost:8080/_admin/crawl?source_id=$SOURCE_ID"

# Wait ~30s, then verify:

# 1. Variant landed in Iceberg
psql "$ICEBERG_CATALOG_URI" -c "
  SELECT kind, COUNT(*) FROM iceberg.opportunities.variants
  WHERE scraped_at > NOW() - INTERVAL '10 minutes'
  GROUP BY kind;
"
# Expect: kind=job count > 0

# 2. Record visible in Manticore
curl -s "$MANTICORE_URL/sql?mode=raw" -d "query=SELECT id, kind, title, issuing_entity FROM idx_opportunities_rt WHERE kind='job' LIMIT 3"
# Expect: 3 rows

# 3. Slug-direct R2 file exists
aws s3 ls s3://opportunities-content/jobs/ --endpoint-url=$R2_ENDPOINT | head -5
# Expect: at least one .json file from this run

# 4. Telemetry counter incremented
curl -s "$OPENOBSERVE_URL/api/default/_search" -H "Authorization: Basic $OO_AUTH" \
    --data-binary '{"query":{"sql":"SELECT * FROM \"pipeline.opportunities.ready\" WHERE _timestamp > now() - INTERVAL 10 MINUTE LIMIT 5"}}'
# Expect: counter rows with kind="job"

# 5. Verify rejection counter (should be 0 unless something failed)
curl -s "$OPENOBSERVE_URL/api/default/_search" -H "Authorization: Basic $OO_AUTH" \
    --data-binary '{"query":{"sql":"SELECT * FROM \"pipeline.verify.rejections\" WHERE _timestamp > now() - INTERVAL 10 MINUTE LIMIT 5"}}'
# Expect: empty or low count
```

If any step fails, check the corresponding service's logs and the `opportunities.variants_rejected` Iceberg table for clues.

---

## Step 9 — Scholarship pilot launch (optional, after Step 8 passes)

```bash
# Register the DAAD scholarship source
curl -s -X POST "$API_URL/_admin/sources" \
    -H 'content-type: application/json' \
    -d "$(<seeds/scholarships-daad.json)"

# Trigger a manual crawl
SOURCE_ID=$(psql "$DATABASE_URL" -t -c "SELECT id FROM sources WHERE id='daad-scholarships'" | xargs)
kubectl -n opportunities exec deployment/opportunities-crawler -- \
    curl -s -X POST "http://localhost:8080/_admin/crawl?source_id=$SOURCE_ID"

# Wait 60s, verify scholarship records appeared:
psql "$ICEBERG_CATALOG_URI" -c "
  SELECT kind, title, issuing_entity FROM iceberg.opportunities.variants
  WHERE kind='scholarship' AND scraped_at > NOW() - INTERVAL '5 minutes'
  LIMIT 3;
"

curl -s "$MANTICORE_URL/sql?mode=raw" \
    -d "query=SELECT id, kind, title, field_of_study, degree_level FROM idx_opportunities_rt WHERE kind='scholarship' LIMIT 3"

aws s3 ls s3://opportunities-content/scholarships/ --endpoint-url=$R2_ENDPOINT | head
```

If scholarship records appear with non-empty `field_of_study` and `degree_level` in Manticore, the polymorphic pipeline is fully validated end-to-end.

---

## Rollback

If Step 7 surfaces a critical bug:

```bash
# Pin all images back to v7.x (whatever the last known-good tag was)
# in the ImagePolicy. FluxCD will roll workloads back automatically.

# Then unwind the schema changes:
# 1. Drop new Iceberg tables (variants_rejected won't exist in v7 schema)
psql "$ICEBERG_CATALOG_URI" -c "DROP TABLE IF EXISTS iceberg.opportunities.variants_rejected"

# 2. Drop new Manticore index, let v7 recreate the old one
curl -s "$MANTICORE_URL/sql?mode=raw" -d "query=DROP TABLE IF EXISTS idx_opportunities_rt"

# 3. NATS stream rename is reversible — recreate svc_opportunities_candidates:
# (the v7 crawler's queue_setup.yaml will recreate it on boot)

# 4. Source table schema rollback (rare; Postgres will tolerate the extra
# columns being unused, so leaving them is usually fine)

# 5. Drop the ConfigMap (v7 doesn't read it)
kubectl -n opportunities delete configmap opportunity-kinds
```

The greenfield-cut nature of v8 means rollback is destructive: any data ingested under v8 is lost. This is acceptable because the system is greenfield and the rollback would only happen within hours of cutover (before meaningful data accumulation).

---

## Verification checklist

- [ ] All six v8 pods running and Ready
- [ ] `opportunity registry: loaded` log line shows 5 kinds in every app
- [ ] `kubectl get configmaps opportunity-kinds -n opportunities` shows 5 YAMLs
- [ ] `nats stream info svc_opportunities_matching` shows healthy stream
- [ ] Iceberg `opportunities.variants` schema has `kind` + `attributes` columns
- [ ] Manticore `idx_opportunities_rt` has `kind` + sparse-facet columns
- [ ] One smoke crawl produced records at every stage (Iceberg, Manticore, R2)
- [ ] OpenObserve shows `pipeline.opportunities.ready{kind="job"}` non-zero
- [ ] No records in `opportunities.variants_rejected` (or only expected mismatches)
- [ ] (Optional) DAAD scholarship pilot produced kind=scholarship records end-to-end
