# stawi.opportunities

Job and opportunity discovery platform: structured crawl → PostgreSQL → matching → paid delivery.

## Services

| Binary | Role |
|--------|------|
| `apps/crawler` | Source schedules, structured crawl (API / schema.org / recipes), admit/enqueue |
| `apps/frontier-worker` | Optional URL frontier: fetch detail pages, extract JobPosting JSON-LD |
| `apps/worker` | Drain `job_ingest_queue` → merge into `opportunities` |
| `apps/api` | Public search + admin source control plane |
| `apps/matching` | Candidates, CV pipeline, matching, billing, digests |
| `apps/applications` | Application tracking API (optional deploy) |

Scheduling and maintenance run via **Trustage** workflows (`definitions/trustage/`).

## Extraction policy

**Structured extracts only.** There is no crawl-time universal AI / URL-stub path.

Jobs are stored only when a complete record passes `pkg/crawlaccept` (title, description ≥50 chars, issuing_entity, apply_url):

- JSON API connectors (RemoteOK, Arbeitnow, Jobicy, TheMuse, Himalayas, …)
- Workday / SmartRecruiters
- Sitemap discovery + schema.org JobPosting on detail pages
- HTML JSON-LD (`pkg/connectors/structured`)
- Active extraction recipes (`pkg/recipe`)
- Declarative R2 spec connectors

LLM is used for **recipe generation** and candidate CV work, not for inventing job listings.

See [docs/ops/crawl-framework.md](docs/ops/crawl-framework.md) and [docs/ops/crawl-pipeline.md](docs/ops/crawl-pipeline.md).

## Quick start

```bash
make infra-up          # Postgres (Timescale + pgvector)
make deps
# Run migrations via crawler/matching with DO_DATABASE_MIGRATE=true (see first-deploy runbook)
make run-crawler       # after workers are up
make run-worker
make run-api
```

## API (high level)

- Search: `GET /api/search?q=…`
- Health: `GET /healthz`
- Matching candidate surface: `/me/*` (JWT)
- Crawl admin: `/admin/*` on crawler/api

## Docs

| Doc | Purpose |
|-----|---------|
| [docs/ops/data-storage.md](docs/ops/data-storage.md) | Full storage map, retention, robustness scorecard |
| [docs/ops/crawl-pipeline.md](docs/ops/crawl-pipeline.md) | End-to-end crawl → store |
| [docs/ops/crawl-framework.md](docs/ops/crawl-framework.md) | How to onboard sources (recipes, structured extract) |
| [docs/ops/first-deploy-runbook.md](docs/ops/first-deploy-runbook.md) | First deploy order |
| [docs/ops/capacity-planning.md](docs/ops/capacity-planning.md) | Queue / worker sizing |
| [docs/billing/provisioning.md](docs/billing/provisioning.md) | Payment catalog setup |
