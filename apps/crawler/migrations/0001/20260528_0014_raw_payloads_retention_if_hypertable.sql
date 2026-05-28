-- lean-plan1: 14 d retention on raw_payloads IF promoted to a
-- hypertable. Same guard pattern as 0013.

DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM timescaledb_information.hypertables
    WHERE hypertable_name = 'raw_payloads'
  ) THEN
    PERFORM remove_retention_policy('raw_payloads', if_exists => TRUE);
    PERFORM add_retention_policy('raw_payloads', INTERVAL '14 days', if_not_exists => TRUE);
  END IF;
END $$;
