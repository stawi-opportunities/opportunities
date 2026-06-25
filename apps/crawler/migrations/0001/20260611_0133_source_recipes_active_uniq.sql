-- One active recipe per source. AutoMigrate creates source_recipes from the
-- SourceRecipe model, but GORM tags can't express a WHERE-filtered (partial)
-- unique index, so it lives here as a migration file. Runs after AutoMigrate
-- (pool.Migrate AutoMigrates before applying files), never a Go db.Exec helper.
CREATE UNIQUE INDEX IF NOT EXISTS idx_source_recipes_active
    ON source_recipes (source_id) WHERE status = 'active';
