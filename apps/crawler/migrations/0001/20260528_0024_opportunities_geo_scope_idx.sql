CREATE INDEX IF NOT EXISTS opportunities_geo_scope_idx
    ON opportunities (geo_scope)
    WHERE hidden = false AND status = 'active' AND geo_scope IS NOT NULL;
