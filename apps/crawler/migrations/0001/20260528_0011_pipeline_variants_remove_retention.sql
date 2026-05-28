-- lean-plan1: remove any pre-existing retention policy on
-- pipeline_variants so the next migration can add the 7 d window
-- cleanly. if_exists => TRUE keeps re-runs idempotent.

SELECT remove_retention_policy('pipeline_variants', if_exists => TRUE);
