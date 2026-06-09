-- Active recipe lives inline on the source (queryable: "which sources have a recipe?").
ALTER TABLE sources
    ADD COLUMN IF NOT EXISTS extraction_recipe JSONB NOT NULL DEFAULT '{}'::jsonb;

-- Full version history for diff + rollback.
CREATE TABLE IF NOT EXISTS source_recipes (
    id                VARCHAR(20)      PRIMARY KEY,
    source_id         VARCHAR(20)      NOT NULL,
    version           INTEGER          NOT NULL,
    recipe            JSONB            NOT NULL,
    status            VARCHAR(16)      NOT NULL DEFAULT 'active',  -- active | superseded | rejected
    pass_rate         DOUBLE PRECISION NOT NULL DEFAULT 0,
    model             VARCHAR(128)     NOT NULL DEFAULT '',
    validation_report JSONB,
    created_at        TIMESTAMPTZ      NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_source_recipes_source ON source_recipes (source_id, version DESC);
-- At most one active recipe per source.
CREATE UNIQUE INDEX IF NOT EXISTS idx_source_recipes_active ON source_recipes (source_id) WHERE status = 'active';
