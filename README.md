# stawi.opportunities ingestion platform

Production-oriented job ingestion stack in Go for high-coverage global job discovery.

## Services
- `source-discovery`: manages and seeds source registry
- `scheduler`: schedules due sources and publishes crawl requests
- `worker`: executes source connectors, normalize -> dedupe -> index flow
- `dedupe`: background dedupe maintenance runner
- `search-api`: query canonical jobs
- `ops-control-plane`: source inventory and health endpoints

## Quick start
1. `cp .env.example .env` and export variables.
2. `make infra-up`
3. `make deps`
4. `make migrate`
5. Run services in separate terminals:
   - `make run-discovery`
   - `make run-scheduler`
   - `make run-worker`
   - `make run-search`
   - `make run-ops`

## API
- Search: `GET /search?q=backend&limit=20`
- Ops sources: `GET /sources?limit=100`
- Health: `GET /healthz`

## Connectors
Implemented connectors (4 APIs + 8 crawlers):
- Adzuna, SerpApi, USAJOBS, SmartRecruiters API
- Greenhouse, Lever, Workday, SmartRecruiters page crawler
- Schema.org JobPosting crawler
- Sitemap crawler
- Hosted boards crawler
- Generic HTML crawler
