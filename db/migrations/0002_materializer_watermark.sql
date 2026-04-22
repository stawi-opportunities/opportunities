-- Per-prefix watermark for apps/materializer. One row per
-- collection prefix (e.g. "canonicals", "embeddings"). last_r2_key
-- is the most-recently-processed R2 object key; subsequent polls
-- use it as ListObjectsV2 StartAfter.
--
-- Small table, low write volume — one UPDATE per poll tick per
-- collection. Postgres is fine; a future phase may migrate to KV.
--
-- DEPRECATED (v6.0.0): apps/materializer now uses Iceberg snapshot-diff
-- reads with Valkey watermarks (mat:snap:<table> keys). This table is
-- no longer written by the materializer. The CREATE TABLE IF NOT EXISTS
-- is harmless on existing clusters; new clusters may leave it empty.

CREATE TABLE IF NOT EXISTS materializer_watermarks (
    prefix        text PRIMARY KEY,
    last_r2_key   text NOT NULL DEFAULT '',
    updated_at    timestamptz NOT NULL DEFAULT now()
);
