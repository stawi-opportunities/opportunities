-- how_to_apply lives on product Neon (matching / product catalog).
-- Kept as a no-op on crawl Neon so historical migration versions remain
-- idempotent when re-applied against either database.

DO $mig$
BEGIN
  IF to_regclass('public.opportunities') IS NOT NULL THEN
    EXECUTE 'ALTER TABLE opportunities ADD COLUMN IF NOT EXISTS how_to_apply text';
  ELSE
    RAISE NOTICE 'opportunities table absent (crawl Neon) — how_to_apply skipped';
  END IF;
END
$mig$;
