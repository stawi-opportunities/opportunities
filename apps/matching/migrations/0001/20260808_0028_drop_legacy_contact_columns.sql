-- Remove plaintext contact storage from candidate_profiles.
-- Contacts live in ProfileService (identity attached + CV standalone IDs).

ALTER TABLE candidate_profiles DROP COLUMN IF EXISTS phone;
ALTER TABLE candidate_profiles DROP COLUMN IF EXISTS emails;
