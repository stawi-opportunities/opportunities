# First-Deploy Runbook

**Owner:** Platform / Peter Bwire
**Audience:** On-call operator deploying stawi.opportunities to a fresh cluster (or after full DR recovery)
**Estimated total time:** 1–2 hours
**Rollback:** revert deployment manifests + redeploy previous image tag

> This document supersedes `docs/ops/cutover-runbook.md`, which is retained as a
> historical reference for the Phase 5→6 migration. For greenfield deploys (new
> cluster or DR recovery) use this document.

---

## 0. Prerequisites

- [ ] `kubectl` context points at the target cluster and is tested: `kubectl get nodes`
- [ ] Flux is running: `kubectl get helmreleases -n opportunities`
- [ ] Managed infra ready:
  - [ ] Manticore StatefulSet running: `kubectl get pods -n opportunities -l app=manticore`
  - [ ] Valkey reachable from within the cluster
  - [ ] R2 bucket `opportunities-log` created with credentials at hand
  - [ ] TEI embed/rerank endpoints answer `/health`
- [ ] NATS JetStream provisioned: `kubectl get pods -n queue-system`
- [ ] All six app images built and pushed for the target SHA

---

## 1. Bootstrap (run once)

```bash
# Copy and fill secrets
cp scripts/bootstrap/vault-seeds.env.example scripts/bootstrap/vault-seeds.env
$EDITOR scripts/bootstrap/vault-seeds.env

# Run the full bootstrap sequence
chmod +x scripts/bootstrap/*.sh
./scripts/bootstrap/bootstrap.sh
```

The bootstrap script runs:
1. `seed-vault.sh` — writes Vault paths for iceberg-catalog + r2-log-credentials
2. Postgres migrations (manual step; follow the on-screen prompt)
3. `create-iceberg.sh` — creates Iceberg namespaces and tables via a k8s Job
4. `create-manticore-schema.sh` — applies `idx_opportunities_rt` DDL to Manticore

See `scripts/bootstrap/README.md` for common failure modes.

---

## 2. Apply deployment manifests

```bash
cd /home/j/code/antinvestor/deployments

# Apply the opportunities namespace manifests
kubectl apply -k manifests/namespaces/opportunities/

# Force Flux reconciliation
flux reconcile kustomization opportunities --with-source
```

Wait for all six pods to reach `Ready`:
```bash
kubectl get pods -n opportunities -w
```

Expected: `api`, `writer`, `materializer`, `crawler`, `worker`, `candidates` — all `Running 1/1`.

---

## 3. Wait for images

If Flux image automation is active it will update image tags automatically.
Check current tags:
```bash
kubectl get imagepolicies -n opportunities
```

To pin to a specific SHA:
```bash
# Edit the tag comment in the relevant HelmRelease, e.g.:
#   tag: v6.3.0 # {"$imagepolicy": "opportunities:opportunities-api:tag"}
```

---

## 4. Verify endpoints

```bash
API_URL=https://opportunities.stawi.org  # or internal URL during staging

# Health check
curl -sf "${API_URL}/healthz" | jq .

# Search returns results
curl -sf "${API_URL}/api/v2/search?q=engineer&limit=5" | jq '.hits | length'
```

Expected: `healthz` returns `{"status":"ok"}` (or similar); search returns ≥ 0 hits
(may be 0 before the fill checkpoint).

---

## 5. Apply Trustage triggers

```bash
# From the repo root
for trigger in \
    definitions/trustage/writer-compact.json \
    definitions/trustage/writer-expire-snapshots.json \
    definitions/trustage/sources-quality-window-reset.json \
    definitions/trustage/sources-health-decay.json \
    definitions/trustage/candidates-matches-weekly-digest.json \
    definitions/trustage/candidates-cv-stale-nudge.json
do
    trustage deploy "${trigger}"
done
```

---

## 6. Fill checkpoint (1–3 hours for 1M+ jobs)

Wait for `idx_opportunities_rt` row count to cross the launch threshold (default 50k):

```bash
# Poll row count via API
watch -n 30 "curl -sf ${API_URL}/healthz | jq .total_jobs"

# Alternatively, poll Manticore directly
kubectl -n opportunities exec -i svc/manticore -- \
    /usr/bin/mysql -h127.0.0.1 -P9306 -e "SELECT COUNT(*) FROM idx_opportunities_rt;"

# Tail writer flush events
kubectl logs -f -l app=opportunities-writer -n opportunities | grep "parquet flushed"

# Tail materializer upserts
kubectl logs -f -l app=opportunities-materializer -n opportunities | grep "manticore upsert"
```

Post progress to #stawi-ops every 30 minutes.

---

## 7. Smoke test

```bash
# Full search + detail smoke
curl -sf "${API_URL}/api/v2/search?q=software+engineer&limit=10" | jq '.hits[0].slug' | \
    xargs -I{} curl -sf "${API_URL}/api/v2/jobs/{}" | jq .title

# Category filter
curl -sf "${API_URL}/api/v2/search?category=engineering&limit=5" | jq '.hits | length'

# Country filter
curl -sf "${API_URL}/api/v2/search?country=KE&limit=5" | jq '.hits | length'

# Remote filter
curl -sf "${API_URL}/api/v2/search?remote_type=remote&limit=5" | jq '.hits | length'
```

All requests should return HTTP 200. Result counts may be 0 before fill.

---

## 3. Rollback

If any step above fails before a stable state is established:

1. Scale all six apps to 0:
   ```bash
   for app in api writer materializer crawler worker candidates; do
       kubectl scale deploy/opportunities-${app} --replicas=0 -n opportunities
   done
   ```
2. Revert HelmRelease image tags to the last known-good version (e.g., `v5.0.2`).
3. Apply the manifests: `kubectl apply -k manifests/namespaces/opportunities/`
4. Investigate, fix, and redeploy.

There is no "point of no return" on a greenfield deploy — Iceberg tables and
Manticore can be rebuilt idempotently (see `scripts/bootstrap/`).

---

## 4. Post-deploy verification (24 hours)

- [ ] `tests/k6/smoke_post_cut.js` passes against the live environment
- [ ] Manticore query p95 < 100 ms
- [ ] Writer ack-lag p95 < 30 s
- [ ] Materializer upsert-lag p95 < 30 s
- [ ] No unresolved alerts in OpenObserve
- [ ] `idx_opportunities_rt` row count growing monotonically

---

## 5. Resource scaling

See `docs/ops/capacity-planning.md` and `manifests/namespaces/opportunities/common/RESOURCE_SCALING.md`.

All services use `pkg/memconfig` adaptive sizing — raise `limits.memory` to increase
throughput; lower it to reduce resource usage. No code changes required.
