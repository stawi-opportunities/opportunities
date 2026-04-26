# Iceberg table definitions

Scripts to provision the stawi.opportunities Iceberg catalog. Run once per environment (dev/staging/prod).

## Prerequisites

- Postgres reachable with iceberg_* catalog tables migrated (see `db/migrations/0004_iceberg_catalog.sql`)
- R2 credentials for the log bucket
- Python 3.11+

## Run

    pip install -r requirements.txt

    export ICEBERG_CATALOG_URI="postgresql+psycopg2://$DATABASE_USERNAME:$DATABASE_PASSWORD@$DATABASE_HOST:5432/$DATABASE_NAME"
    export R2_ACCESS_KEY_ID=...
    export R2_SECRET_ACCESS_KEY=...
    export R2_LOG_BUCKET=opportunities-log
    export R2_ENDPOINT=https://$R2_ACCOUNT_ID.r2.cloudflarestorage.com

    python create_namespaces.py
    python create_tables.py

## Idempotency

Both scripts skip existing namespaces/tables. Safe to re-run after schema bumps — but note that
Iceberg partition specs are NOT easily evolvable once created. Bucket counts especially cannot
change without table recreation.
