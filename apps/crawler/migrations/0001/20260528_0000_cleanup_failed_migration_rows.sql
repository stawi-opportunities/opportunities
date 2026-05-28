-- Cleanup tracking rows for migrations that failed under the
-- previous (multi-statement) shape. Frame's pool.Migrate uses
-- tx.Exec which prepares the SQL — Postgres prepared statements
-- only allow ONE command per Exec, so the multi-statement files
-- in commits 31db3d4 / 935baa0 / 0857e73 / ff8a634 all errored
-- with SQLSTATE 42601 (cannot insert multiple commands into a
-- prepared statement) and left applied_at = NULL.
--
-- Replacing those files with single-statement equivalents creates
-- new tracking rows; the original rows would still be selected
-- by ApplyNewMigrations (WHERE applied_at IS NULL) and re-tried
-- on every boot. Drop them up-front so the new sequence runs
-- clean.
--
-- Single statement. Safe to re-run (the DELETE matches nothing
-- on a fresh cluster).

DELETE FROM migrations WHERE applied_at IS NULL AND name IN (
    'string:migration.sql',
    'string:20260528_pipeline_variants_ledger_links.sql',
    'string:20260528_lean_plan1_retention_and_rejected.sql',
    'string:20260528_lean_plan2_opportunities_hot_fields.sql',
    'string:20260528_lean_plan3_candidate_lean.sql'
);
