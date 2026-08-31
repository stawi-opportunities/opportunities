# Schema evolution

Add timestamped SQL migrations under the owning service's `migrations/0001`
directory. The crawler owns **crawl Neon** tables (sources, frontier, ingest
queue). Matching owns **product Neon** tables (catalog, candidates,
applications, matching, billing cache). Separate Neon projects — never share.
See [db-boundaries.md](./db-boundaries.md).

Use ordinary PostgreSQL tables for mutable state. Time-ordered operational
history lives in plain append-only tables with trigger guards (partitioning
via pg_partman can be revisited if volume warrants). Every migration must be
idempotent and integration tested against the production PostgreSQL major
version.
