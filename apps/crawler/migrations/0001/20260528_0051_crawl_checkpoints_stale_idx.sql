-- Index for the stale-checkpoint sweep: lets ops scan checkpoints
-- older than the StaleAfter window in O(log n) when diagnosing
-- stuck resume loops. The resume hot path already keys by primary
-- key (source_id, connector_type), so this index is purely for the
-- /admin/checkpoints LIST + maintenance queries.

CREATE INDEX IF NOT EXISTS crawl_checkpoints_stale_idx
    ON crawl_checkpoints (last_checkpoint_at);
