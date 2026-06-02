# R2 Data Catalog Migration

**Date:** 2026-05-27
**Status:** In progress

## Context

The writer service uses Apache Iceberg (via `iceberg-go`) to persist
pipeline events as Parquet to R2. The metadata catalog is currently
self-hosted Lakekeeper — a dedicated Rust service + its own CNPG Postgres
cluster in the `lakehouse` namespace (4 pods, ~250m CPU, ~450MB RAM).

Cloudflare now offers R2 Data Catalog: a managed Iceberg REST catalog
built into R2 buckets. Same REST interface, zero infrastructure to run.

## Decision

Replace Lakekeeper with R2 Data Catalog on the `cluster-chronicle` bucket.

## What changes

### 1. Enable R2 Data Catalog on the bucket

```bash
npx wrangler r2 bucket catalog enable cluster-chronicle
```

Returns the catalog URI and warehouse name. The catalog endpoint
is a standard Iceberg REST API — `iceberg-go`'s `restcat.NewCatalog`
speaks to it without code changes.

### 2. Update `pkg/icebergclient/catalog.go`

Rename Lakekeeper-specific naming to catalog-agnostic. The function
signature and behaviour stay the same — `restcat.NewCatalog` with
URI + bearer token. Only comments and doc references change.

### 3. Rewrite `apps/writer/cmd/bootstrap.go`

Replace the Lakekeeper management API calls (waitForLakekeeper,
acceptBootstrapTerms, ensureWarehouse) with a call to the Cloudflare
R2 Data Catalog API:

```
POST /accounts/{account_id}/r2-catalog/{bucket_name}/enable
```

Then use the standard Iceberg REST catalog API (via `iceberg-go`) to
create namespaces and tables — this path is already implemented and
unchanged.

### 4. Update writer config

Add `CLOUDFLARE_API_TOKEN` for the catalog management API (bootstrap only).
Change `ICEBERG_CATALOG_URI` default to the R2 catalog endpoint.
The runtime `ICEBERG_CATALOG_TOKEN` uses the same Cloudflare API token
(bearer auth on the REST catalog).

### 5. Update deployment manifests

- Writer + bootstrap Job: new env vars pointing to R2 catalog
- Remove the Lakekeeper Kustomization from the lakehouse namespace
- Remove the lakehouse CNPG cluster dependency (the DB only existed
  for Lakekeeper metadata)
- Remove the `writer-compact.json` and `writer-expire-snapshots.json`
  Trustage cron definitions (R2 catalog handles both automatically)

### 6. Remove Lakekeeper infrastructure

After verifying the writer boots and commits with R2 catalog:
- Delete the lakehouse namespace Kustomizations
- Delete the CNPG cluster, poolers, secrets

## What stays the same

- `iceberg-go` REST catalog client (`restcat.NewCatalog`)
- All table schemas in `pkg/icebergclient/schemas.go`
- The writer service itself (buffer, flush, commit logic)
- R2 storage (Parquet files stay in `cluster-chronicle`)
- Namespace structure (`opportunities.*`, `candidates.*`)

## Risk

Low. R2 Data Catalog is public beta but exposes the same Iceberg REST
spec that `iceberg-go` already speaks. The Parquet data files don't move.
If R2 catalog has issues, we can re-deploy Lakekeeper and point back.
