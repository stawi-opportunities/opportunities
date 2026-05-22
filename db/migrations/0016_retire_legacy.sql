-- 0016: retire legacy schema (spec §5.6).
--
-- candidate_matches_legacy: holds rows that the GORM-managed
--   CandidateMatch struct used to write. Renamed out of the way in
--   migration 0013; pre-launch, nothing depends on it. Drop.
--
-- candidate_profiles.embedding: moved to candidate_match_indexes by
--   Phase 1. The column has been read-only since then. Drop the
--   column to free up bytes per row.

DROP TABLE IF EXISTS candidate_matches_legacy;

ALTER TABLE candidate_profiles
    DROP COLUMN IF EXISTS embedding;
