CREATE INDEX IF NOT EXISTS pipeline_variants_raw_payload_idx
    ON pipeline_variants (raw_payload_id)
    WHERE raw_payload_id IS NOT NULL;
