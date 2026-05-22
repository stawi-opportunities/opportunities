-- 0017: candidate onboarding draft column.
-- The wizard saves a per-step JSONB blob here so the user resumes
-- at the step they left, on any device, with their answers intact.
-- Cleared back to '{}'::jsonb when POST /candidates/onboard finalises.
-- See docs/superpowers/specs/2026-05-23-paid-flow-routing-and-opportunities-feed-design.md

ALTER TABLE candidate_profiles
  ADD COLUMN IF NOT EXISTS onboarding_draft JSONB NOT NULL DEFAULT '{}'::jsonb;
