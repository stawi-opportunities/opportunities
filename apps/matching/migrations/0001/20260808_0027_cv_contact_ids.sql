-- Standalone ProfileService contact_ids discovered from CVs.
-- Not profile-attached identity contacts (those stay on ProfileService
-- profile.contacts[] for checkout/notify only).

ALTER TABLE candidate_profiles
  ADD COLUMN IF NOT EXISTS cv_contact_ids text[] NOT NULL DEFAULT '{}';
