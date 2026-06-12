-- Hard single-flight for resumable crawls: at most one in-progress run per
-- source. AutoMigrate creates crawl_runs from the domain.CrawlRun model, but
-- GORM tags can't express a WHERE-filtered (partial) unique index, so it lives
-- here as a migration file. Runs after AutoMigrate (pool.Migrate AutoMigrates
-- before applying files), never a Go db.Exec helper.
CREATE UNIQUE INDEX IF NOT EXISTS idx_crawl_runs_active
    ON crawl_runs (source_id) WHERE status IN ('running', 'paused');
