-- lean-plan3: slim candidate_profiles.
--   - DROP cv_raw_text (zero production writers; dead schema).
--   - ADD cv_storage_uri + cv_content_hash (R2 path pointer for
--     the forthcoming CV-upload wiring).
--   - SWAP four skill columns from CSV TEXT to text[] with
--     USING CASE so NULL/empty becomes ARRAY[]::text[], not ARRAY[''].
--
-- candidate_profiles is owned by the matching service's AutoMigrate.
-- This migration may run before matching has booted, so the whole
-- body is guarded by an existence check on the table.
--
-- Whole file is ONE DO $$ ... $$ block = one PostgreSQL statement.

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_tables
    WHERE schemaname = 'public' AND tablename = 'candidate_profiles'
  ) THEN
    RAISE NOTICE 'candidate_profiles not yet present; skipping lean-plan3 alter';
    RETURN;
  END IF;

  ALTER TABLE candidate_profiles
      DROP COLUMN IF EXISTS cv_raw_text;

  ALTER TABLE candidate_profiles
      ADD COLUMN IF NOT EXISTS cv_storage_uri  TEXT,
      ADD COLUMN IF NOT EXISTS cv_content_hash VARCHAR(64);

  IF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'candidate_profiles' AND column_name = 'skills' AND data_type = 'text'
  ) THEN
    ALTER TABLE candidate_profiles
        ALTER COLUMN skills TYPE text[]
        USING CASE WHEN COALESCE(skills, '') = '' THEN ARRAY[]::text[]
                   ELSE string_to_array(skills, ',') END;
  END IF;

  IF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'candidate_profiles' AND column_name = 'strong_skills' AND data_type = 'text'
  ) THEN
    ALTER TABLE candidate_profiles
        ALTER COLUMN strong_skills TYPE text[]
        USING CASE WHEN COALESCE(strong_skills, '') = '' THEN ARRAY[]::text[]
                   ELSE string_to_array(strong_skills, ',') END;
  END IF;

  IF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'candidate_profiles' AND column_name = 'working_skills' AND data_type = 'text'
  ) THEN
    ALTER TABLE candidate_profiles
        ALTER COLUMN working_skills TYPE text[]
        USING CASE WHEN COALESCE(working_skills, '') = '' THEN ARRAY[]::text[]
                   ELSE string_to_array(working_skills, ',') END;
  END IF;

  IF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'candidate_profiles' AND column_name = 'tools_frameworks' AND data_type = 'text'
  ) THEN
    ALTER TABLE candidate_profiles
        ALTER COLUMN tools_frameworks TYPE text[]
        USING CASE WHEN COALESCE(tools_frameworks, '') = '' THEN ARRAY[]::text[]
                   ELSE string_to_array(tools_frameworks, ',') END;
  END IF;
END $$;
