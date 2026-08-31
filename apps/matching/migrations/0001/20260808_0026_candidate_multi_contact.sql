-- Multi-contact support for CV hub: phone may hold several numbers;
-- emails holds all emails found on the CV (CSV). Auth profile remains
-- canonical for login email; this is CV contact display/autofill only.
--
-- Both columns were later removed again (20260808_0028 drops them), and
-- current GORM models no longer create phone at all, so on a greenfield
-- replay the column may not exist. Guard the ALTER on column existence;
-- the frame migrator tracks migrations by filename only (no checksum),
-- so already-applied databases are unaffected by this edit.

DO $mig$
BEGIN
  IF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'candidate_profiles'
      AND column_name = 'phone'
  ) THEN
    EXECUTE 'ALTER TABLE candidate_profiles
               ALTER COLUMN phone TYPE text USING phone::text';
  END IF;
END
$mig$;

ALTER TABLE candidate_profiles
  ADD COLUMN IF NOT EXISTS emails text NOT NULL DEFAULT '';
