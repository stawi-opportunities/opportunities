-- Per-prefix watermark for apps/materializer. One row per
-- collection prefix (e.g. "canonicals", "embeddings"). last_r2_key
-- is the most-recently-processed R2 object key; subsequent polls
-- use it as ListObjectsV2 StartAfter.
--
-- Small table, low write volume — one UPDATE per poll tick per
-- collection. Postgres is fine; a future phase may move this to
-- the KV for crisper ops bundling.

CREATE TABLE IF NOT EXISTS materializer_watermarks (
    prefix        text PRIMARY KEY,
    last_r2_key   text NOT NULL DEFAULT '',
    updated_at    timestamptz NOT NULL DEFAULT now()
);
