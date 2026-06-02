-- lean-plan1: clear the rejected-variant backlog.
-- publishRejected used to write a terminal-stage row to
-- pipeline_variants for every Verify-failure; the durable record
-- lives in the Iceberg opportunities.variants_rejected table.
-- Idempotent (re-runs match nothing).

DELETE FROM pipeline_variants WHERE current_stage = 'rejected';
