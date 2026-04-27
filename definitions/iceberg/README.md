# Iceberg table definitions (DEPRECATED — see below)

The Python scripts in this directory are kept as historical reference.
The canonical Iceberg layout (warehouse, namespaces, tables) is now
materialised in Go and provisioned automatically by the
`opportunities-iceberg-bootstrap` Kubernetes Job on every FluxCD
reconcile.

## Where the source of truth lives

- Schemas: `pkg/icebergclient/schemas.go`
- Bootstrap entry point: `apps/writer/cmd/bootstrap.go`
- Job manifest: `deployment.manifests/namespaces/product-opportunities/bootstrap/`

## Why these scripts still exist

`_schemas.py` is preserved as analyst-facing documentation: pyiceberg
schema literals are easy to read alongside Iceberg's spec.
`create_tables.py` and `create_namespaces.py` document the partition
specs, sort orders, and bloom-filter properties used in production.
None of them are executed by any deployment path.

## Required R2 buckets (operator action)

The bootstrap process assumes three Cloudflare R2 buckets exist:

| Bucket | Purpose |
|---|---|
| `cluster-chronicle` | Lakekeeper warehouse (Iceberg data + metadata) |
| `product-opportunities-content` | Public job/opportunity slug-direct JSONs |
| `product-opportunities-archive` | Private raw bodies + JSON bundles |

The Vault paths under `stawi-opportunities/opportunities/common/` keep
their original names; the bucket *values* inside those secrets are the
new names above.
