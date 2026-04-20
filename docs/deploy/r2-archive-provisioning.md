# R2 Archive Bucket Provisioning

One-time operator steps required before the R2 blob archive (spec
`2026-04-20-r2-blob-archive-design.md`) can run in production.

## 1. Create the private bucket

In the Cloudflare dashboard (or via wrangler):

- **Bucket name:** `stawi-jobs-archive`
- **Public access:** disabled. This bucket holds raw HTML + cluster
  bundles; it must not be addressable via a custom domain and must
  never be exposed through a CDN.
- **Location hint:** same as the public `job-repo` bucket to keep
  intra-region latency low.

Apply the R2 lifecycle rule (JSON configuration or dashboard):

```json
{
  "rules": [
    {
      "id": "cold-tier-after-30d",
      "enabled": true,
      "filter": { "prefix": "" },
      "transitions": [
        {
          "storageClass": "InfrequentAccess",
          "condition": { "ageDays": 30 }
        }
      ]
    }
  ]
}
```

**No deletion rule.** Retention is lifecycle-driven (purge sweeper
fires only when `canonical_jobs.status = 'deleted'` + 7-day grace).

## 2. Mint scoped access credentials

Create a new R2 API token scoped only to `stawi-jobs-archive`:

- Permissions: Object Read & Write + Bucket Admin (for `ListObjectsV2`
  and `DeleteObjects`).
- No access to the public `stawi-jobs-content` bucket.

Record:

- `ARCHIVE_R2_ACCOUNT_ID` (same Cloudflare account ID used for the
  public bucket)
- `ARCHIVE_R2_ACCESS_KEY_ID`
- `ARCHIVE_R2_SECRET_ACCESS_KEY`
- `ARCHIVE_R2_BUCKET` = `stawi-jobs-archive`

## 3. Store credentials in Vault

Path: `secret/antinvestor/stawi-jobs/common/archive-r2-credentials`
(mirrors the existing `secret/antinvestor/stawi-jobs/common/r2-credentials`
layout used for the public bucket).

Keys (match the public bucket's property names so the ExternalSecret
shape stays consistent):

```
r2_account_id         = <from step 2>
r2_access_key_id      = <from step 2>
r2_secret_access_key  = <from step 2>
r2_bucket             = stawi-jobs-archive
```

Use the `vault-secret` skill at
`/home/j/code/antinvestor/deployments/.claude/skills/vault-secret/`
(kubectl exec into `vault-openbao-0` with a freshly-minted
`external-secrets` SA token; `bao` CLI, not `vault`). The external-
secrets policy has CRUD on all of `secret/data/*` so no policy change
is needed for the new path.

Optional: if the Cloudflare dashboard API token is also handed over at
provisioning, store it alongside at
`secret/antinvestor/stawi-jobs/common/cloudflare-api` with key
`api_token`. The crawler does not consume it directly — it's for
future automation (bucket CRUD, Pages deploys).

## 4. Wire the ExternalSecret

In the GitOps repo (`antinvestor/deployments`), add a new
`ExternalSecret` named `archive-r2-credentials-stawi-jobs` mirroring
the existing `r2-credentials-stawi-jobs` that sources the public
bucket's creds:

```yaml
# New ExternalSecret for the archive bucket.
- secretKey: ARCHIVE_R2_ACCOUNT_ID
  remoteRef:
    key: antinvestor/stawi-jobs/common/archive-r2-credentials
    property: r2_account_id
- secretKey: ARCHIVE_R2_ACCESS_KEY_ID
  remoteRef:
    key: antinvestor/stawi-jobs/common/archive-r2-credentials
    property: r2_access_key_id
- secretKey: ARCHIVE_R2_SECRET_ACCESS_KEY
  remoteRef:
    key: antinvestor/stawi-jobs/common/archive-r2-credentials
    property: r2_secret_access_key
- secretKey: ARCHIVE_R2_BUCKET
  remoteRef:
    key: antinvestor/stawi-jobs/common/archive-r2-credentials
    property: r2_bucket
```

The Go config layer (`apps/crawler/config/config.go`) consumes
`ARCHIVE_R2_*` via the `env:` tags defined in T7. No code change on
bump.

## 5. Verify post-deploy

After the crawler pod has rolled with the new secret:

```bash
# Tail logs for archive construction.
kubectl logs -n stawi-jobs deploy/stawi-jobs-crawler | grep -i archive

# Trigger a crawl and confirm raw_payloads + raw/ in R2.
kubectl exec -n stawi-jobs deploy/stawi-jobs-crawler -- \
  curl -sS -X POST http://localhost:8080/admin/crawl/dispatch-due?limit=1

# After ~30s, sample the archive.
ARCHIVE_R2_BUCKET=stawi-jobs-archive \
ARCHIVE_R2_ENDPOINT=https://<account>.r2.cloudflarestorage.com \
AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=... \
  make archive-verify SAMPLE=10
```

`archive-verify` should exit 0 with "10 canonicals checked, all blobs
present."

## 6. Schedule Trustage crons

The cron definitions (`definitions/trustage/r2-purge.json` and
`r2-reconcile.json`) ship with this repo. Trustage picks them up on
its next config reload — no manual registration needed if the
deployment is wired up the usual way.

Verify:

```bash
# Trustage admin UI → Schedules → filter stawi-jobs.r2.*
```

Both entries should show `active: true` with next-run timestamps.

## 7. Rollback

If the archive bucket needs to be disabled in a hurry:

1. Revoke the R2 API token (Cloudflare dashboard).
2. The crawler's next PutRaw will fail, aborting variant ingest. No
   data is lost — NATS queue buffers events until the token is
   rotated back in.
3. For cluster bundles already in R2, nothing auto-deletes them —
   they persist until the purge sweeper runs against deleted
   canonicals.

The public `job-repo` bucket and production traffic are unaffected by
archive outages — archive is strictly internal.
