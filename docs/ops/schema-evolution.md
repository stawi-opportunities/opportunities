# Schema evolution

Add timestamped SQL migrations under the owning service's `migrations/0001`
directory. The crawler owns opportunity ingestion and serving tables; matching
owns candidate and application tables.

Use ordinary PostgreSQL tables for mutable state. Use a TimescaleDB hypertable
only for time-ordered operational history, and enforce append-only behavior for
immutable event ledgers. Every migration must be idempotent and integration
tested against the production TimescaleDB major version.
