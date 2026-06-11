-- pg_trgm extension — required for fuzzy-search indexes. GORM AutoMigrate
-- can't add extensions, so it lives here. Was previously applied by the
-- repository.FinalizeSchema Go helper; moved to a migration file so all
-- schema flows through the migrator.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
