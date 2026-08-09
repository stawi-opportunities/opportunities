-- Structured education entries (school/degree/field/dates/notes).
-- Free-text education remains as a compact multi-line summary.

ALTER TABLE candidate_profiles
  ADD COLUMN IF NOT EXISTS education_history jsonb NOT NULL DEFAULT '[]'::jsonb;
