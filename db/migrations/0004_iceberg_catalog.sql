-- db/migrations/0004_iceberg_catalog.sql
-- Iceberg JDBC/SQL catalog tables. These back the Iceberg catalog service
-- used by apps/writer, apps/worker, apps/materializer, apps/candidates,
-- apps/api, and apps/iceberg-ops. Hosting them on the existing stawi_jobs
-- Postgres avoids a second stateful service.
--
-- Both iceberg-go's catalog/sql (dialect: postgres) and pyiceberg's
-- SqlCatalog expect these exact table names and column shapes.

CREATE TABLE IF NOT EXISTS iceberg_tables (
    catalog_name                TEXT NOT NULL,
    table_namespace             TEXT NOT NULL,
    table_name                  TEXT NOT NULL,
    metadata_location           TEXT,
    previous_metadata_location  TEXT,
    iceberg_type                TEXT,
    PRIMARY KEY (catalog_name, table_namespace, table_name)
);

CREATE INDEX IF NOT EXISTS idx_iceberg_tables_catalog
    ON iceberg_tables (catalog_name);

CREATE TABLE IF NOT EXISTS iceberg_namespace_properties (
    catalog_name    TEXT NOT NULL,
    namespace       TEXT NOT NULL,
    property_key    TEXT NOT NULL,
    property_value  TEXT,
    PRIMARY KEY (catalog_name, namespace, property_key)
);

CREATE INDEX IF NOT EXISTS idx_iceberg_ns_props_catalog
    ON iceberg_namespace_properties (catalog_name);
