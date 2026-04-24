# Bootstrap Scripts

Run once per environment — new cluster, disaster-recovery rebuild, or staging reset.
Every script is idempotent: re-running is safe.

## When to run

| Situation | What to run |
|-----------|-------------|
| First deploy on a new cluster | `bootstrap.sh` (full sequence) |
| DR recovery after full cluster loss | `bootstrap.sh` (full sequence) |
| Re-seed Vault after secret rotation | `seed-vault.sh` only |
| Rebuild Iceberg tables after catalog wipe | `create-iceberg.sh` only |
| Rebuild Manticore schema after index wipe | `create-manticore-schema.sh` only |

See `docs/ops/first-deploy-runbook.md` for the complete production checklist.
See `docs/ops/disaster-recovery-runbook.md` for per-scenario DR procedures.

## Prerequisites

- `kubectl` configured with cluster access (context pointing at the target cluster)
- `vault-seeds.env` filled in (copy from `vault-seeds.env.example`)
- The `external-secrets` ServiceAccount exists in the `external-secrets` namespace
- The `vault-openbao-0` pod is running in `vault-system`

## Quick start

```bash
# 1. Copy and fill secrets file
cp scripts/bootstrap/vault-seeds.env.example scripts/bootstrap/vault-seeds.env
$EDITOR scripts/bootstrap/vault-seeds.env

# 2. Run full bootstrap
chmod +x scripts/bootstrap/*.sh
./scripts/bootstrap/bootstrap.sh
```

## Script reference

### `seed-vault.sh`

Reads `vault-seeds.env` and writes secrets to Vault via the cluster K8s-auth
pattern (ServiceAccount token → `vault-openbao-0` pod → `bao kv put`).

Writes:
- `secret/antinvestor/stawi-jobs/common/iceberg-catalog` — Iceberg SQL catalog DSN
- `secret/antinvestor/stawi-jobs/common/r2-log-credentials` — R2 event-log credentials

After seeding, ExternalSecrets sync within their `refreshInterval` (default 1h).
Force immediate sync with:
```bash
kubectl annotate externalsecret iceberg-catalog-credentials-stawi-jobs \
  -n stawi-jobs reconcile.external-secrets.io/trigger=$(date +%s) --overwrite
kubectl annotate externalsecret r2-log-credentials-stawi-jobs \
  -n stawi-jobs reconcile.external-secrets.io/trigger=$(date +%s) --overwrite
```

**Common failures:**
- `ERROR: vault-seeds.env not found` — copy the example file and fill values
- `bao write auth/kubernetes/login: permission denied` — the `external-secrets` SA
  may not exist yet; apply the external-secrets HelmRelease first
- `connection refused` to `vault-openbao-0` — check vault pod is Running:
  `kubectl get pods -n vault-system`

### `create-iceberg.sh`

Submits a one-shot Kubernetes `batch/v1 Job` to the `stawi-jobs` namespace.
The job clones this repo, installs `pyiceberg`, and runs:
1. `definitions/iceberg/create_namespaces.py`
2. `definitions/iceberg/create_tables.py`

**Prerequisites:** ExternalSecrets `iceberg-catalog-credentials-stawi-jobs` and
`r2-log-credentials-stawi-jobs` must be synced before this runs.

**Common failures:**
- `ImagePullBackOff` on `python:3.12-slim` — cluster has no outbound internet; use
  a mirrored image or pre-pull to a private registry
- `git clone` fails — firewall blocks GitHub; seed `/tmp/sj` via a ConfigMap instead
- pyiceberg SQL catalog `could not connect` — check ICEBERG_CATALOG_URI points to
  a reachable pooler RW endpoint from within the cluster

### `create-manticore-schema.sh`

Pipes `definitions/manticore/idx_jobs_rt.sql` into Manticore via:
```bash
kubectl -n stawi-jobs exec -i svc/manticore -- /usr/bin/mysql -h127.0.0.1 -P9306 < DDL_FILE
```

Pass an alternative DDL file as `$1` if you need to apply a schema variant.

**Common failures:**
- `svc/manticore not found` — Manticore StatefulSet not yet running; wait for
  the HelmRelease to reconcile
- `table already exists` — safe to ignore; the materializer's `searchindex.Apply`
  also handles this gracefully

### `bootstrap.sh`

Composed entry point. Runs all four steps in order with a manual confirmation
gate before the Postgres migration step.

## Security notes

- `vault-seeds.env` is git-ignored and must never be committed.
- The only secrets stored in Git are SOPS-encrypted bootstrap files
  (`vault-credentials.yaml`, `vault-unseal-key.yaml`) in the deployments repo.
- After seeding, rotate the R2 access keys in the Cloudflare dashboard and
  re-run `seed-vault.sh` to update Vault.
