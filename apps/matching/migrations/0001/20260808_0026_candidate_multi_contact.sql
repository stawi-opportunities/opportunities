-- Multi-contact support for CV hub: phone may hold several numbers;
-- emails holds all emails found on the CV (CSV). Auth profile remains
-- canonical for login email; this is CV contact display/autofill only.

ALTER TABLE candidate_profiles
  ALTER COLUMN phone TYPE text USING phone::text;

ALTER TABLE candidate_profiles
  ADD COLUMN IF NOT EXISTS emails text NOT NULL DEFAULT '';
