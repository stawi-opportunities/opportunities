# Rename Runbook: stawi.jobs → stawi-opportunities/opportunities

**Date:** 2026-04-22  
**Scope:** Code rename is complete (automated). The following 8 steps are manual operator actions required to complete the cutover.

---

## Step 1 — Create the `stawi-opportunities` GitHub org

1. Log in to GitHub with an account that can create organisations.
2. Navigate to <https://github.com/organizations/plan> and create the org `stawi-opportunities`.
3. Configure billing, default visibility, and base permissions as appropriate.

---

## Step 2 — Transfer or re-create the repo

**Option A — Transfer from antinvestor:**

```bash
gh repo transfer antinvestor/stawi-jobs stawi-opportunities
```

Then rename:

```bash
gh repo rename --repo stawi-opportunities/stawi-jobs opportunities
```

**Option B — Fresh create + push** (avoids needing transfer permissions):

```bash
gh repo create stawi-opportunities/opportunities --public
git -C /home/j/code/stawi.jobs remote set-url origin https://github.com/stawi-opportunities/opportunities.git
git -C /home/j/code/stawi.jobs push -u origin main
```

---

## Step 3 — Build and push v7.0.0 images

After the rename commit lands on `main` and CI is configured for the new org:

```bash
# images to publish:
ghcr.io/stawi-opportunities/opportunities-api:v7.0.0
ghcr.io/stawi-opportunities/opportunities-candidates:v7.0.0
ghcr.io/stawi-opportunities/opportunities-crawler:v7.0.0
ghcr.io/stawi-opportunities/opportunities-materializer:v7.0.0
ghcr.io/stawi-opportunities/opportunities-worker:v7.0.0
ghcr.io/stawi-opportunities/opportunities-writer:v7.0.0
```

Verify each image is pullable before proceeding to cluster cutover.

---

## Step 4 — Cluster cutover

```bash
# 1. Scale down old namespace gracefully
kubectl -n stawi-jobs scale deployment --all --replicas=0

# 2. Apply new namespace manifests
kubectl apply -k /path/to/deployments/manifests/namespaces/opportunities/

# 3. Verify all pods reach Running
kubectl -n opportunities get pods -w

# 4. Smoke-test endpoints
curl -s https://opportunities.stawi.org/api/v1/health

# 5. Repoint DNS / Cloudflare Pages: opportunities.stawi.org becomes the
# canonical custom domain on the Pages project; jobs.stawi.org stays as a
# CNAME alias issuing a 301 to opportunities.stawi.org. (See Step 8.)

# 6. Delete old namespace once traffic migrated
kubectl delete namespace stawi-jobs
```

---

## Step 5 — Vault path migration

Re-seed secrets under the new path prefix `stawi-opportunities/opportunities/...`.  
The old prefix was `antinvestor/stawi-jobs/...`.

```bash
# Run the updated seed script (seeds under new paths automatically)
./scripts/bootstrap/seed-vault.sh

# Trigger ExternalSecret reconciliation in the opportunities namespace
kubectl annotate externalsecret iceberg-catalog-credentials-opportunities \
    -n opportunities reconcile.external-secrets.io/trigger=$(date +%s) --overwrite
kubectl annotate externalsecret r2-log-credentials-opportunities \
    -n opportunities reconcile.external-secrets.io/trigger=$(date +%s) --overwrite
```

After verifying secrets are synced, optionally delete old Vault paths:

```bash
# Only after new paths are confirmed working
bao kv delete secret/antinvestor/stawi-jobs/common/iceberg-catalog
bao kv delete secret/antinvestor/stawi-jobs/common/r2-log-credentials
```

---

## Step 6 — GHCR cleanup

Once the new images at `ghcr.io/stawi-opportunities/opportunities-*` are live and
all services are healthy, delete the old packages:

```bash
for svc in api candidates crawler materializer worker writer; do
    gh api --method DELETE \
        repos/antinvestor/stawi-jobs-${svc}/packages/container/stawi-jobs-${svc}
done
```

Check GHCR package visibility settings for the new org too.

---

## Step 7 — Postgres DB rename

The application now expects the database to be named `opportunities` (was `stawi_jobs`).

```bash
# Option A: rename in place (brief downtime)
kubectl -n datastore exec -i svc/pooler-rw -- psql -U postgres <<'SQL'
ALTER DATABASE stawi_jobs RENAME TO opportunities;
SQL

# Option B: dump-restore (zero-downtime blue/green)
pg_dump -Fc stawi_jobs > stawi_jobs.dump
createdb opportunities
pg_restore -d opportunities stawi_jobs.dump
```

Update the Iceberg catalog `uri` secret in Vault to reference `opportunities` instead of `stawi_jobs`:

```bash
bao kv patch secret/stawi-opportunities/opportunities/common/iceberg-catalog \
    uri="postgres://user:pass@pooler-rw.datastore.svc:5432/opportunities?sslmode=require"
```

---

## Step 8 — Cloudflare Pages rename

The Pages project was previously `stawi-jobs` (serving `jobs.stawi.org`).

**Domain decision:** `opportunities.stawi.org` is the canonical public domain.
`jobs.stawi.org` becomes a **CNAME alias** pointing at the same Pages project so
existing bookmarks, OAuth callbacks, and SEO links keep resolving — but every
canonical link, sitemap entry, OIDC redirect URI, and User-Agent now points at
`opportunities.stawi.org`.

1. In the Cloudflare dashboard, rename the project or create a new one named `opportunities`.
2. Add **both** custom domains to the Pages project:
   - `opportunities.stawi.org` (canonical — primary domain in Pages settings)
   - `jobs.stawi.org` (alias — CNAME at the DNS layer pointing to the same project)
3. Configure the redirect rule so `jobs.stawi.org/*` issues HTTP 301 →
   `opportunities.stawi.org/$1` for SEO consolidation. Cloudflare Pages
   "Bulk redirects" or a `_redirects` file at the site root both work; pick
   whichever the existing project already uses.
4. Update the CF Pages deploy hook URL in Vault (see `r2_deploy_hook.md`):
   ```bash
   bao kv patch secret/stawi-opportunities/opportunities/common/cf-pages \
       deploy_hook_url="https://api.cloudflare.com/client/v4/pages/webhooks/deploy_hooks/<new-hook-id>"
   ```
5. After the new domain is live, register the second OIDC redirect URI in
   service-authentication so logins from either origin resolve cleanly:
   ```sql
   -- in service-authentication's partitions table, append the alias to the
   -- redirect_uris JSON array on the Stawi Opportunities partition row.
   UPDATE partitions
   SET    redirect_uris = '{"uris": ["https://opportunities.stawi.org/auth/callback/", "https://jobs.stawi.org/auth/callback/"]}'
   WHERE  partition_id  = 'd7gi6lkpf2t67dlsqreg';
   ```

---

## Verification checklist

- [ ] New GitHub org `stawi-opportunities` exists
- [ ] Repo `stawi-opportunities/opportunities` is accessible
- [ ] Images `ghcr.io/stawi-opportunities/opportunities-*:v7.0.0` are published
- [ ] k8s namespace `opportunities` has all pods Running
- [ ] `opportunities.stawi.org` resolves and returns HTTP 200
- [ ] Vault secrets at `stawi-opportunities/opportunities/...` are synced via ExternalSecrets
- [ ] Old `stawi-jobs` namespace deleted
- [ ] Old GHCR packages deleted or archived
- [ ] Postgres DB renamed to `opportunities`
- [ ] Cloudflare Pages serving from `opportunities.stawi.org`
- [ ] `jobs.stawi.org` resolves and 301s to `opportunities.stawi.org`
- [ ] OIDC partition has both redirect URIs registered
