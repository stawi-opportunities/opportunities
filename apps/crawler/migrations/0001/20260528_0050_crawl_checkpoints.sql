-- Iterator checkpoint store. One row per (source_id, connector_type)
-- — the latest cursor + page index the connector emitted on the last
-- successful page. Persisted after every successful page so a
-- restart picks up from the same spot.
--
-- Schema notes:
--   - cursor is JSONB so per-connector cursor shapes don't need
--     migrations as new connectors land.
--   - last_url is informational (operator drill-down); not used by
--     the resume path.
--   - last_checkpoint_at drives TTL: a checkpoint older than 6h is
--     stale (the source's listing-page state has likely shifted)
--     and the resume path discards it, falling back to a fresh crawl.

CREATE TABLE IF NOT EXISTS crawl_checkpoints (
    source_id           VARCHAR(20)  NOT NULL,
    connector_type      TEXT         NOT NULL,
    cursor              JSONB        NOT NULL DEFAULT '{}'::jsonb,
    page_idx            INTEGER      NOT NULL DEFAULT 0,
    last_url            TEXT,
    last_checkpoint_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (source_id, connector_type)
);
