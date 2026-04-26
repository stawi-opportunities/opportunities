# Deployment: jobs.stawi.org

## Overview

Deploy the stawi.opportunities UI as a fully functional static site on Cloudflare Pages at `jobs.stawi.org`. Jobs are published to Cloudflare R2 as individual markdown files in real-time via an event-driven pipeline handler. OIDC authentication uses a dedicated child tenant and partition under Thesa in service-authentication. The site rebuilds automatically when new content is available.

## Architecture

```
Crawl Pipeline
  ...
  variant.validated → CanonicalHandler
                         ↓
                    job.ready event
                         ↓
                    PublishHandler (NEW)
                      1. Load CanonicalJob
                      2. Generate permanent slug
                      3. Render markdown + frontmatter
                      4. PutObject to R2: jobs/{slug}.md
                         ↓
                    (periodic deploy hook trigger)
                         ↓
                    CF Pages Build
                      1. sync-r2.sh: aws s3 sync R2 → content/jobs/
                      2. hugo --minify
                         ↓
                    jobs.stawi.org (Cloudflare edge)
```

## 1. Permanent Job Slugs

### Design Principle

A slug is the job's permanent public identity. It must be:
- **Unique** across all jobs, forever
- **Stable** — never changes once assigned, even if the title or company is edited
- **Human-readable** — contains the job title and company for SEO
- **URL-safe** — lowercase, hyphens only, no special characters

### Slug Format

```
{slugified-title}-at-{slugified-company}-{short-hash}
```

Examples:
- `senior-go-developer-at-acme-corp-a3f7b2`
- `product-designer-at-safari-tech-e91c4d`
- `data-engineer-at-finflow-8b2e1a`

The `{short-hash}` is the first 6 characters of `SHA256(company + "|" + title + "|" + canonical_job_id)`. This ensures uniqueness even when two companies post the same title, and is deterministic (same inputs always produce the same hash).

### Storage

Add a `Slug` field to `CanonicalJob`:

```go
Slug string `gorm:"type:varchar(255);uniqueIndex" json:"slug"`
```

The slug is generated **once** when the canonical job is first created (in the CanonicalHandler or PublishHandler). Once set, it never changes. GORM AutoMigrate handles the column addition.

### Slug Generation Function

Add to `pkg/domain/models.go`:

```go
func BuildSlug(title, company string, id int64) string {
    slugTitle := Slugify(title)
    slugCompany := Slugify(company)
    hash := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d", company, title, id)))
    shortHash := hex.EncodeToString(hash[:3]) // 6 hex chars
    slug := fmt.Sprintf("%s-at-%s-%s", slugTitle, slugCompany, shortHash)
    if len(slug) > 250 {
        slug = slug[:250]
    }
    return slug
}

func Slugify(s string) string {
    s = strings.ToLower(strings.TrimSpace(s))
    replacer := strings.NewReplacer(
        " ", "-", "/", "-", "\\", "-", ".", "-",
        ",", "", "'", "", "\"", "", "(", "", ")", "",
        "&", "and", "+", "plus",
    )
    s = replacer.Replace(s)
    for strings.Contains(s, "--") {
        s = strings.ReplaceAll(s, "--", "-")
    }
    return strings.Trim(s, "-")
}
```

### Where Slugs Are Set

In the CanonicalHandler, after `dedupeEngine.UpsertAndCluster` returns a canonical job, if `canonical.Slug == ""`, generate and persist it:

```go
if canonical != nil && canonical.Slug == "" {
    canonical.Slug = domain.BuildSlug(canonical.Title, canonical.Company, canonical.ID)
    jobRepo.UpdateCanonicalFields(ctx, canonical.ID, map[string]any{"slug": canonical.Slug})
}
```

This ensures the slug is set once and never overwritten.

## 2. PublishHandler — Event-Driven R2 Upload

### Handler Registration

New file: `pkg/pipeline/handlers/publish.go`

Subscribes to `job.ready` event. Registered in the crawler alongside the other handlers.

### Handler Logic

```
1. Receive JobReadyPayload{CanonicalJobID}
2. Load CanonicalJob from DB (needs slug, title, company, description, etc.)
3. Skip if quality_score < 50 (below publish threshold)
4. Skip if !IsActive
5. Render markdown file:
   ---
   title: "Senior Go Developer"
   date: 2026-04-15T10:00:00Z
   slug: "senior-go-developer-at-acme-corp-a3f7b2"
   params:
     id: 12345
     company: "Acme Corp"
     category: "programming"
     location_text: "Remote - Anywhere"
     remote_type: "fully_remote"
     employment_type: "Full-Time"
     salary_min: 80000
     salary_max: 120000
     currency: "USD"
     seniority: "senior"
     skills: ["Go", "PostgreSQL", "Kubernetes"]
     apply_url: "https://acme.com/apply/123"
     quality_score: 88.5
     is_featured: true
   ---
   
   Full job description in markdown...
6. Upload to R2: PutObject("jobs/{slug}.md", markdownBytes)
```

### R2 Client

Use `aws-sdk-go-v2` with R2's S3-compatible API:

```go
type R2Publisher struct {
    client *s3.Client
    bucket string
}

func NewR2Publisher(accountID, accessKeyID, secretKey, bucket string) *R2Publisher
func (p *R2Publisher) PublishJob(ctx context.Context, job *domain.CanonicalJob) error
func (p *R2Publisher) DeleteJob(ctx context.Context, slug string) error
func (p *R2Publisher) PublishStats(ctx context.Context, stats StatsData) error
func (p *R2Publisher) PublishCategories(ctx context.Context, categories []CategoryData) error
```

### Configuration

New env vars for the crawler:

```
R2_ACCOUNT_ID=<cloudflare account id>
R2_ACCESS_KEY_ID=<r2 api token key>
R2_SECRET_ACCESS_KEY=<r2 api token secret>
R2_BUCKET=opportunities-content
R2_DEPLOY_HOOK_URL=<cf pages deploy hook url>
PUBLISH_MIN_QUALITY=50
```

### Deploy Hook Triggering

The PublishHandler does NOT trigger the deploy hook on every job. Instead:

- The PublishHandler tracks how many jobs it has published since the last hook trigger using an atomic counter.
- After every 50 published jobs, or when the crawler signals end-of-cycle, it POSTs to the deploy hook URL.
- This batches rebuilds so the site rebuilds a few times per crawl cycle, not 37K times.

### Unpublish Flow

When a job becomes inactive (`IsActive = false`), the PublishHandler should delete the markdown file from R2:
- Subscribe to a new `job.deactivated` event (emitted when a job is marked inactive during recrawl)
- Call `r2Publisher.DeleteJob(ctx, job.Slug)`

## 3. OIDC — New Tenant + Partition in service-authentication

### Migration Files

Two new SQL files in `/home/j/code/antinvestor/service-authentication/apps/tenancy/migrations/0001/`:

#### `20260416_create_stawi_jobs_tenant.sql` (Production)

```sql
-- Tenant: Stawi Jobs (production)
INSERT INTO tenants (id, tenant_id, partition_id, name, description, environment)
VALUES (
    '<new-xid-tenant>',
    'c2f4j7au6s7f91uqnojg',    -- parent tenant: Thesa
    'c2f4j7au6s7f91uqnokg',    -- parent partition: Thesa
    'Stawi Jobs',
    'Remote job board platform for Africa',
    'production'
) ON CONFLICT (id) DO NOTHING;

-- Partition: Stawi Jobs (production)
INSERT INTO partitions (id, tenant_id, partition_id, parent_id, name, description, domain, allow_auto_access, properties)
VALUES (
    '<new-xid-partition>',
    '<new-xid-tenant>',
    '<new-xid-partition>',       -- self-referential
    'c2f4j7au6s7f91uqnokg',     -- parent: Thesa partition
    'Stawi Jobs',
    'Remote job board platform',
    'jobs.stawi.org',
    'true',
    '{}'
) ON CONFLICT (id) DO NOTHING;

-- OIDC Client: Stawi Jobs Web (production, public)
INSERT INTO clients (
    id, tenant_id, partition_id, name, client_id,
    type, grant_types, response_types, scopes, audiences,
    redirect_uris, post_logout_redirect_uris, token_endpoint_auth_method
) VALUES (
    '<new-xid-client>',
    '<new-xid-tenant>',
    '<new-xid-partition>',
    'Stawi Jobs Web',
    'opportunities-web',
    'public',
    '{"types": ["authorization_code", "refresh_token"]}',
    '{"types": ["code"]}',
    'openid offline_access profile',
    '{}',
    '{"uris": ["https://jobs.stawi.org/auth/callback/"]}',
    '{"uris": ["https://jobs.stawi.org/"]}',
    'none'
) ON CONFLICT (id) DO NOTHING;
```

#### `20260416_create_stawi_jobs_test_tenant.sql` (Staging)

Same structure with:
- `environment: 'staging'`
- Different XIDs
- `domain: 'jobs-dev.stawi.org'`
- `redirect_uris: ["https://jobs-dev.stawi.org/auth/callback/", "http://localhost:1313/auth/callback/"]`
- `client_id: 'opportunities-web-dev'`

### Hugo Config Updates

Update `ui/hugo.toml` to use the staging OIDC client by default:

```toml
[params]
  oidcIssuer = "https://auth.antinvestor.com/realms/opportunities"
  oidcClientID = "opportunities-web-dev"
  oidcRedirectURI = "http://localhost:1313/auth/callback/"
```

CF Pages environment variables override these for production:
- `HUGO_PARAMS_oidcIssuer=https://auth.antinvestor.com/realms/opportunities`
- `HUGO_PARAMS_oidcClientID=opportunities-web`
- `HUGO_PARAMS_oidcRedirectURI=https://jobs.stawi.org/auth/callback/`

## 4. Cloudflare Pages Deployment

### Project Setup

- **Source**: GitHub repo `opportunities`, branch `main`
- **Root directory**: `ui`
- **Build command**: `chmod +x scripts/sync-r2.sh && ./scripts/sync-r2.sh && hugo --minify --baseURL https://jobs.stawi.org`
- **Build output**: `public`
- **Hugo version**: Set via `HUGO_VERSION=0.147.0` env var (CF Pages installs it)

### Environment Variables (CF Pages Dashboard)

| Variable | Production | Preview |
|----------|-----------|---------|
| `HUGO_VERSION` | `0.147.0` | `0.147.0` |
| `AWS_ACCESS_KEY_ID` | R2 key | R2 key |
| `AWS_SECRET_ACCESS_KEY` | R2 secret | R2 secret |
| `R2_ACCOUNT_ID` | CF account ID | CF account ID |
| `R2_BUCKET` | `opportunities-content` | `opportunities-content` |

OIDC params are passed via `HUGO_PARAMS_*` env vars which Hugo reads automatically.

### Custom Domains

- **Production**: `jobs.stawi.org` → CNAME to CF Pages project
- **Staging**: `jobs-dev.stawi.org` → CF Pages preview branch

### Pre-Build Sync Script

`ui/scripts/sync-r2.sh`:

```bash
#!/bin/bash
set -euo pipefail

echo "Installing AWS CLI..."
pip install awscli --quiet 2>/dev/null

R2_ENDPOINT="https://${R2_ACCOUNT_ID}.r2.cloudflarestorage.com"

echo "Syncing job content from R2..."
aws s3 sync "s3://${R2_BUCKET}/jobs/" content/jobs/ \
    --endpoint-url "$R2_ENDPOINT" --no-sign-request=false

echo "Syncing category content from R2..."
aws s3 sync "s3://${R2_BUCKET}/categories/" content/categories/ \
    --endpoint-url "$R2_ENDPOINT" --no-sign-request=false

echo "Syncing data files from R2..."
mkdir -p data
aws s3 cp "s3://${R2_BUCKET}/data/stats.json" data/stats.json \
    --endpoint-url "$R2_ENDPOINT" --no-sign-request=false || echo "{}" > data/stats.json
aws s3 cp "s3://${R2_BUCKET}/data/categories.json" data/categories.json \
    --endpoint-url "$R2_ENDPOINT" --no-sign-request=false || echo "[]" > data/categories.json

echo "Sync complete. $(find content/jobs -name '*.md' 2>/dev/null | wc -l) job files."
```

### GitHub Workflow for UI

Add `.github/workflows/pages.yaml`:

```yaml
name: Deploy to Cloudflare Pages
on:
  push:
    branches: [main]
    paths: ['ui/**']
  workflow_dispatch:

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      # CF Pages handles the build via its own GitHub integration
      # This workflow only runs if we need to do something extra
      # The primary deployment is via CF Pages GitHub integration
```

In practice, CF Pages GitHub integration handles deployment automatically on push. The workflow file is optional — only needed if we add extra steps.

## 5. Sitegen CLI — Backfill Mode

The `sitegen` CLI remains useful for:
- **Initial backfill**: Upload all 37K existing jobs to R2 before the event-driven flow takes over
- **Regenration**: If slug format changes or we need to republish everything

Update `apps/sitegen/cmd/main.go` to support R2 upload mode:

```
Usage: sitegen [flags]
  --database-url     Postgres connection string
  --output-dir       Write JSON to local dir (existing behavior)
  --r2-upload        Upload markdown files to R2 instead of local JSON
  --r2-account-id    Cloudflare account ID
  --r2-access-key    R2 access key
  --r2-secret-key    R2 secret key
  --r2-bucket        R2 bucket name (default: opportunities-content)
  --min-quality      Minimum quality score (default: 50)
  --batch-size       Upload batch size (default: 500)
  --deploy-hook-url  CF Pages deploy hook to trigger after upload
```

When `--r2-upload` is set, sitegen:
1. Queries jobs in batches of 500
2. For each batch: generates markdown, uploads to R2 via S3 PutObject
3. After all batches: uploads stats.json + categories.json to R2
4. Triggers deploy hook

## 6. Hugo Template Updates for Slug-Based URLs

Job pages use the slug as their URL path. Since content files are named `{slug}.md`, Hugo automatically uses the filename as the URL.

Update `ui/layouts/jobs/single.html` — the JSON-LD and all internal links use the slug-based permalink:

```html
<link rel="canonical" href="{{ .Permalink }}">
<!-- .Permalink will be /jobs/{slug}/ -->
```

Update `ui/layouts/partials/job-card.html` — links already use `.Permalink` which resolves correctly.

The dashboard and search results that link to jobs by ID need a mapping. Since the slug is in the frontmatter (`params.id` maps to canonical ID), the SPA islands can construct the URL from the slug directly when it's returned by the API. Add `slug` to the API response for canonical jobs.

## Summary of Changes

### In stawi.opportunities repo:
1. Add `Slug` field to `CanonicalJob` model
2. Add `BuildSlug` and `Slugify` functions to `pkg/domain/models.go`
3. Update `CanonicalHandler` to generate slug on first creation
4. Create `PublishHandler` + `R2Publisher` in `pkg/pipeline/handlers/`
5. Add `EventJobDeactivated` event and wire unpublish flow
6. Update sitegen CLI with `--r2-upload` backfill mode
7. Create `ui/scripts/sync-r2.sh`
8. Update `ui/hugo.toml` with OIDC params
9. Update `.gitignore` for content synced from R2
10. Add `Slug` to API response for canonical jobs

### In service-authentication repo:
11. Create `20260416_create_stawi_jobs_tenant.sql` (production)
12. Create `20260416_create_stawi_jobs_test_tenant.sql` (staging)

### In Cloudflare Dashboard:
13. Create R2 bucket `opportunities-content`
14. Create R2 API token with read/write access
15. Create CF Pages project linked to GitHub
16. Configure custom domain `jobs.stawi.org`
17. Set environment variables
18. Create deploy hook and save URL
