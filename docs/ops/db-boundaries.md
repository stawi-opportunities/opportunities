# Database boundaries (crawl vs product)

## Two databases, two owners

| Database | Runtime | Schema owner | Consumers |
|----------|---------|--------------|-----------|
| **Crawl** (CNPG `product-opportunities-db`) | Cluster | `apps/crawler` migrate job | crawler, frontier-worker, worker (queue only) |
| **Product** (Neon) | Cloud Run | `apps/matching` migrate job | matching, api, worker (catalog write) |

Do **not** point crawler migrations at Neon. Do **not** AutoMigrate crawl queue tables from matching.

## Table ownership

### Crawl DB (crawler)

- Control: `sources`, `source_recipes`, `crawl_runs`, `host_state`
- Work: `url_frontier`, `job_ingest_queue`
- Audit: `crawl_jobs`, `job_ingest_events` (hypertables)

### Product DB (matching)

- Catalog: `opportunity_identities`, `opportunities`, `opportunity_sources`, `companies`, `opportunity_flags`
- Candidates / matches / saved jobs / applications / billing cache

## Cross-plane APIs (only allowed shared surfaces)

1. **Worker dual-DB** (`PRODUCT_DATABASE_URL` set)  
   - Claim/ack/retry on crawl DB  
   - Catalog merge on product DB  
   - Implemented in `pkg/jobqueue.Store.WithProductDB`  
   - Production requires both URLs; no silent single-DB fallback for prod

2. **Pub/Sub fan-out**  
   - `MATCHING_FANOUT_QUEUE_URL=gcppubsub://stawi-opportunities/opportunities-fanout`  
   - Worker publishes after embed; matching consumes Path A

3. **Public HTTP**  
   - Search/detail: Cloud Run `opportunities-api` (`/opportunities`)  
   - Candidate product: Cloud Run `opportunities-matching` (`/matching`)  
   - Crawl admin stays on cluster crawler (not public Cloud Run)

## Env contract

| App | `DATABASE_URL` | `PRODUCT_DATABASE_URL` |
|-----|----------------|------------------------|
| crawler | crawl CNPG | unset |
| frontier-worker | crawl CNPG | unset |
| worker | crawl CNPG | product Neon (**required** in prod) |
| matching (CR) | product Neon | n/a |
| api (CR) | product Neon (same secret as matching) | n/a |

## Deploy reference

- Cluster: `deployment.manifests` `namespaces/product-opportunities/common/CUTOVER_CLOUD_RUN.md`
- Cloud Run: `cloud.deployment` `docs/DEPLOY_OPPORTUNITIES.md`
- Design: `cloud.deployment` `docs/superpowers/specs/2026-07-29-opportunities-cloudrun-neon-design.md`
