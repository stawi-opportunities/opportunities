-- lean-plan3: GIN indexes on the matcher-hot skill columns for
-- @> containment queries. Guarded by the same existence check
-- as 0030.

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
