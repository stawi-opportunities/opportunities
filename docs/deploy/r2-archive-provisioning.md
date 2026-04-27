# R2 Archive Bucket Provisioning

One-time operator steps required before the R2 blob archive (spec
`2026-04-20-r2-blob-archive-design.md`) can run in production.

> **Note (post-consolidation):** As of the credential-consolidation
> change, all three product-opportunities R2 buckets (`cluster-chronicle`,
> `product-opportunities-content`, `product-opportunities-archive`)
> share a **single** Cloudflare R2 account token, seeded once at
> `secret/stawi-opportunities/opportunities/common/r2-account`. There
> is no separate `archive-r2-credentials` Vault path or ExternalSecret
> any more. The bucket-creation steps below still apply; the credential
> steps now point at the shared `r2-account` Vault path.

## 1. Create the private bucket

In the Cloudflare dashboard (or via wrangler):

- **Bucket name:** `product-opportunities-archive`
- **Public access:** disabled. This bucket holds raw HTML + cluster
  bundles; it must not be addressable via a custom domain and must
  never be exposed through a CDN.
- **Location hint:** same as the public content bucket
  (`product-opportunities-content`) to keep intra-region latency low.

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

## 2. Mint the shared account token (if not already done)

The Product Opportunities Account Token must have R2 Read+Write on all
three buckets:

- `cluster-chronicle`
- `product-opportunities-content`
- `product-opportunities-archive`

If the token already exists (the chronicle/content buckets typically
provision it first), nothing to do here — the same key authorises the
archive bucket too.

## 3. Store credentials in Vault

Path: `secret/stawi-opportunities/opportunities/common/r2-account`

Properties (set once for the whole opportunities platform):

```
r2_account_id         = <Cloudflare account ID>
r2_access_key_id      = <token's key ID>
r2_secret_access_key  = <token's secret>
r2_endpoint           = https://<account_id>.r2.cloudflarestorage.com
r2_deploy_hook_url    = <optional CF Pages deploy hook URL>
```

Use the `vault-secret` skill (kubectl exec into `vault-openbao-0` with
a freshly-minted `external-secrets` SA token; `bao` CLI, not `vault`).

Bucket *names* are NOT stored in Vault — they're plain config in each
HelmRelease (`R2_CONTENT_BUCKET`, `R2_ARCHIVE_BUCKET`,
`R2_CHRONICLE_BUCKET`).

## 4. ExternalSecret (already wired)

A single `r2-account-credentials-opportunities` ExternalSecret in
`namespaces/product-opportunities/common/r2-account-credentials.yaml`
projects the Vault path into a Kubernetes Secret. Each HelmRelease
(api, crawler, worker, writer, matching, bootstrap) reads
`R2_ACCOUNT_ID/R2_ACCESS_KEY_ID/R2_SECRET_ACCESS_KEY/R2_ENDPOINT` from
that one Secret. There's no per-bucket ExternalSecret to wire.

## 5. Verify post-deploy

After the crawler pod has rolled with the new secret:

```bash
# Tail logs for archive construction.
kubectl logs -n product-opportunities deploy/opportunities-crawler | grep -i archive

# Trigger a crawl and confirm raw_payloads + raw/ in R2.
kubectl exec -n product-opportunities deploy/opportunities-crawler -- \
  curl -sS -X POST http://localhost:8080/admin/crawl/dispatch-due?limit=1

# After ~30s, sample the archive.
R2_ARCHIVE_BUCKET=product-opportunities-archive \
R2_ENDPOINT=https://<account>.r2.cloudflarestorage.com \
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
# Trustage admin UI → Schedules → filter opportunities.r2.*
```

Both entries should show `active: true` with next-run timestamps.

## 7. Rollback

If the archive bucket needs to be disabled in a hurry:

1. Revoke the shared R2 API token (Cloudflare dashboard). **Note:** this
   also revokes content + chronicle access — there's no longer a
   separate "archive only" key. If you need to rotate without taking
   down ingest, mint a new token first, update Vault, then revoke the
   old one.
2. The crawler's next PutRaw will fail, aborting variant ingest. No
   data is lost — NATS queue buffers events until the token is
   rotated back in.
3. For cluster bundles already in R2, nothing auto-deletes them —
   they persist until the purge sweeper runs against deleted
   canonicals.

The public content bucket and production traffic are unaffected by
archive outages — archive is strictly internal.
