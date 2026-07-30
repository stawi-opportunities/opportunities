# Schema evolution

Add timestamped SQL migrations under the owning service's `migrations/0001`
directory. The crawler owns **crawl DB** tables (sources, frontier, ingest
queue). Matching owns **product Neon** tables (catalog, candidates,
applications, matching, billing cache). See [db-boundaries.md](./db-boundaries.md).

Use ordinary PostgreSQL tables for mutable state. Use a TimescaleDB hypertable
only for time-ordered operational history, and enforce append-only behavior for
immutable event ledgers. Every migration must be idempotent and integration
tested against the production TimescaleDB major version.
