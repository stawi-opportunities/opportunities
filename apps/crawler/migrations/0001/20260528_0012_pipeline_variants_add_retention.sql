-- lean-plan1: 7 d retention on pipeline_variants — operational
-- ledger, not history. Durable event log lives in Iceberg's
-- variants.ingested / variants.rejected / etc topics.

SELECT add_retention_policy('pipeline_variants', INTERVAL '7 days', if_not_exists => TRUE);
