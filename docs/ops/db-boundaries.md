# Database boundaries (crawl vs product)

## Two Neon databases, two owners

| Database | Runtime | Schema owner | Consumers |
|----------|---------|--------------|-----------|
| **Crawl Neon** (`opportunities-crawler-database-url`) | Cluster jobs + migrate Job | `apps/crawler` | crawler, frontier-worker, worker (queue only) |
| **Product Neon** (`opportunities-matching-database-url`) | Cloud Run | `apps/matching` | matching, api, worker (catalog write) |

Do **not** point crawler migrations at product Neon. Do **not** AutoMigrate crawl queue tables from matching.  
Do **not** use one Neon project for both planes.

## Table ownership

### Crawl Neon (crawler)

- Control: `sources`, `source_recipes`, `crawl_runs`, `host_state`
- Work: `url_frontier`, `job_ingest_queue`
- Audit: `crawl_jobs`, `job_ingest_events`

### Product Neon (matching)

- Catalog: `opportunity_identities`, `opportunities`, `opportunity_sources`, `companies`, `opportunity_flags`
- Candidates / matches / saved jobs / applications / billing cache
- Search indexes: `search_tsv`, lakebase BM25, embedding HNSW

## Critical data path (no NATS required)

```text
crawler/frontier → job_ingest_queue (crawl Neon)
       → worker drain → opportunities (product Neon)
       → Pub/Sub opportunities-worker-embed (optional vectors)
       → Pub/Sub opportunities-fanout → matching Path A
```

| Transport | Role |
|-----------|------|
| **Postgres crawl queue** | SoT for accepted extracts (lease, retry, durable) |
| **GCP Pub/Sub** | Burst absorption for embed + match fan-out |
| **NATS** | Non-critical control only (crawl wake-ups, recipe ops). Internal overdue + idle polls cover misses. |

## Cross-plane APIs

1. **Worker dual-DB** (`PRODUCT_DATABASE_URL` set)  
   - Claim/ack/retry on crawl Neon  
   - Catalog merge on product Neon  
   - `pkg/jobqueue.Store.WithProductDB`

2. **Pub/Sub fan-out / embed**  
   - `MATCHING_FANOUT_QUEUE_URL=gcppubsub://stawi-opportunities/opportunities-fanout`  
   - `WORKER_EMBED_QUEUE_URL=gcppubsub://stawi-opportunities/opportunities-worker-embed`  
   - `WORKER_EMBED_SUBSCRIBE_URL=gcppubsub://stawi-opportunities/opportunities-worker-embed-pull`  
   - ADC via `GOOGLE_APPLICATION_CREDENTIALS` (cluster) or Cloud Run SA

3. **Public HTTP**  
   - Search/detail: Cloud Run `opportunities-api` (`/opportunities`)  
   - Candidate product: Cloud Run `opportunities-matching` (`/matching`)

## Env contract

| App | `DATABASE_URL` | `PRODUCT_DATABASE_URL` |
|-----|----------------|------------------------|
| crawler (cluster) | crawl Neon | unset |
| frontier-worker | crawl Neon | unset |
| worker | crawl Neon | product Neon (**required** in prod) |
| matching (CR) | product Neon | n/a |
| api (CR) | product Neon | n/a |
| crawler (CR migrate) | crawl Neon | n/a |

## Deploy reference

- Cluster: `deployment.manifests` `namespaces/product-opportunities/common/CUTOVER_CLOUD_RUN.md`
- Cloud Run: `cloud.deployment` `docs/DEPLOY_OPPORTUNITIES.md`
