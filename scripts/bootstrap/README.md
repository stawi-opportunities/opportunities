# Environment bootstrap

The opportunities platform uses PostgreSQL with TimescaleDB for job data.

1. Copy `vault-seeds.env.example` to the ignored `vault-seeds.env` and supply
   the R2 credentials used only by definitions and candidate documents.
2. Run `./scripts/bootstrap/bootstrap.sh`.
3. Deploy crawler and matching with `DO_DATABASE_MIGRATE=true` and wait for
   their migrations to finish.
4. Start worker replicas, then enable crawl schedules.

Verify ingestion capacity with `GET /admin/crawl/status`. A healthy empty
system reports `paused=false`, `pending=0`, and `oldest_age_seconds=0`.
