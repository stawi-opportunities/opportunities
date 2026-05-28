-- lean-postgres-plan3: slim candidate_profiles.
--   - DROP cv_raw_text (zero production writers; pure dead schema).
--   - ADD cv_storage_uri + cv_content_hash (R2 path pointer for the
--     forthcoming CV-upload wiring).
--   - SWAP four skill columns from CSV TEXT to text[] with GIN indexes
--     on the matcher hot-path columns (strong_skills, working_skills).
--
-- candidate_profiles is owned by the matching service's AutoMigrate.
-- This migration runs from the crawler pre-install hook and may land
-- before the matching service has booted, so the whole body is guarded
-- by an existence check on the table — it no-ops on a cluster where
-- matching hasn't created the table yet, and runs on subsequent boots.
--
-- Idempotent: DROP COLUMN IF EXISTS, ADD COLUMN IF NOT EXISTS,
-- CREATE INDEX IF NOT EXISTS, type swap only runs when the column is
-- still text (re-runs see the already-converted text[] and skip).

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_tables
    WHERE schemaname = 'public' AND tablename = 'candidate_profiles'
  ) THEN
    RAISE NOTICE 'candidate_profiles not yet present; skipping lean-plan3';
    RETURN;
  END IF;

  -- Drop the dead column.
  ALTER TABLE candidate_profiles
      DROP COLUMN IF EXISTS cv_raw_text;

  -- Add R2 pointer columns.
  ALTER TABLE candidate_profiles
      ADD COLUMN IF NOT EXISTS cv_storage_uri  TEXT,
      ADD COLUMN IF NOT EXISTS cv_content_hash VARCHAR(64);

  -- Swap each skill column to text[] only if it's still text.
  -- The USING CASE turns NULL or '' into an empty array (NOT a
  -- one-element array containing "").
  IF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'candidate_profiles'
      AND column_name = 'skills'
      AND data_type = 'text'
  ) THEN
    ALTER TABLE candidate_profiles
        ALTER COLUMN skills TYPE text[]
        USING CASE WHEN COALESCE(skills, '') = '' THEN ARRAY[]::text[]
                   ELSE string_to_array(skills, ',') END;
  END IF;

  IF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'candidate_profiles'
      AND column_name = 'strong_skills'
      AND data_type = 'text'
  ) THEN
    ALTER TABLE candidate_profiles
        ALTER COLUMN strong_skills TYPE text[]
        USING CASE WHEN COALESCE(strong_skills, '') = '' THEN ARRAY[]::text[]
                   ELSE string_to_array(strong_skills, ',') END;
  END IF;

  IF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'candidate_profiles'
      AND column_name = 'working_skills'
      AND data_type = 'text'
  ) THEN
    ALTER TABLE candidate_profiles
        ALTER COLUMN working_skills TYPE text[]
        USING CASE WHEN COALESCE(working_skills, '') = '' THEN ARRAY[]::text[]
                   ELSE string_to_array(working_skills, ',') END;
  END IF;

  IF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'candidate_profiles'
      AND column_name = 'tools_frameworks'
      AND data_type = 'text'
  ) THEN
    ALTER TABLE candidate_profiles
        ALTER COLUMN tools_frameworks TYPE text[]
        USING CASE WHEN COALESCE(tools_frameworks, '') = '' THEN ARRAY[]::text[]
                   ELSE string_to_array(tools_frameworks, ',') END;
  END IF;
END $$;

-- GIN indexes for `@>` containment queries on the matcher hot path.
-- Created outside the DO block because CREATE INDEX IF NOT EXISTS is
-- itself idempotent and clearer at the top level. Guarded by the
-- pg_tables check at the start of the file.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM pg_tables
    WHERE schemaname = 'public' AND tablename = 'candidate_profiles'
  ) THEN
    CREATE INDEX IF NOT EXISTS candidate_profiles_strong_skills_gin_idx
        ON candidate_profiles USING gin (strong_skills);

    CREATE INDEX IF NOT EXISTS candidate_profiles_working_skills_gin_idx
        ON candidate_profiles USING gin (working_skills);
  END IF;
END $$;
