-- Remove plaintext contact storage from candidate_profiles.
-- Contacts live in ProfileService (identity attached + CV standalone IDs).
--
-- Originally this dropped phone/emails. No backfill copies existing values
-- into ProfileService, and production's migrate Job did not actually
-- migrate until 2026-11 (setup-mode bug), so on databases where this file is
-- still pending the drop would destroy CV-discovered contacts. It now
-- archives the columns instead: renamed out of the application's way and
-- made nullable so inserts that no longer mention them succeed. Drop them
-- in a later migration once a backfill has run or the data is deemed
-- disposable. The frame migrator tracks migrations by filename only, so
-- databases that already applied the original drop are unaffected.

DO $mig$
DECLARE
  col text;
BEGIN
  FOREACH col IN ARRAY ARRAY['phone', 'emails'] LOOP
    IF EXISTS (
      SELECT 1 FROM information_schema.columns
      WHERE table_schema = current_schema()
        AND table_name = 'candidate_profiles'
        AND column_name = col
    ) AND NOT EXISTS (
      SELECT 1 FROM information_schema.columns
      WHERE table_schema = current_schema()
        AND table_name = 'candidate_profiles'
        AND column_name = 'legacy_' || col || '_unmigrated'
    ) THEN
      EXECUTE format('ALTER TABLE candidate_profiles RENAME COLUMN %I TO %I',
                     col, 'legacy_' || col || '_unmigrated');
      EXECUTE format('ALTER TABLE candidate_profiles ALTER COLUMN %I DROP NOT NULL',
                     'legacy_' || col || '_unmigrated');
      EXECUTE format('ALTER TABLE candidate_profiles ALTER COLUMN %I DROP DEFAULT',
                     'legacy_' || col || '_unmigrated');
    END IF;
  END LOOP;
END
$mig$;
