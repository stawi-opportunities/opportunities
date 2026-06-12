-- pg_trgm extension — required for fuzzy-search indexes. GORM AutoMigrate
-- can't add extensions, so it lives here as a migration file: all schema
-- flows through the migrator, never a Go db.Exec helper.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
