-- lean-plan2: promote three filter-hot fields from
-- opportunities.attributes (JSONB) to top-level columns.
-- Single ALTER with three ADD COLUMN clauses = one statement.

ALTER TABLE opportunities
    ADD COLUMN IF NOT EXISTS employment_type TEXT,
    ADD COLUMN IF NOT EXISTS seniority       TEXT,
    ADD COLUMN IF NOT EXISTS geo_scope       TEXT;
