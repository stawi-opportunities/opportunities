-- lean-plan2: backfill the three new columns from the existing
-- attributes JSONB. WHERE-clause guard skips rows already
-- populated so re-runs are O(0).

UPDATE opportunities
   SET employment_type = NULLIF(attributes->>'employment_type', ''),
       seniority       = NULLIF(attributes->>'seniority', ''),
       geo_scope       = NULLIF(attributes->>'geo_scope', '')
 WHERE employment_type IS NULL
    OR seniority IS NULL
    OR geo_scope IS NULL;
