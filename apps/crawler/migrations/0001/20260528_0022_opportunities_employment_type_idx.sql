-- lean-plan2: partial B-tree on employment_type, scoped to the
-- same predicate the existing opportunities_kind_country_last_seen_idx
-- uses so the planner can prune candidate rows efficiently.

CREATE INDEX IF NOT EXISTS opportunities_employment_type_idx
    ON opportunities (employment_type)
    WHERE hidden = false AND status = 'active' AND employment_type IS NOT NULL;
