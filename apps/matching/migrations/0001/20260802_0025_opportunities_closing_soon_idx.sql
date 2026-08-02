-- Product Neon: partial index for sort=closing_soon (deadline ASC).
-- Matches activePred() filter used by public search browse lists.

DO $mig$
BEGIN
  EXECUTE 'CREATE INDEX IF NOT EXISTS opportunities_active_closing_soon_idx
             ON opportunities (deadline ASC NULLS LAST, posted_at DESC NULLS LAST)
             WHERE hidden = false AND status = ''active''';
END
$mig$;
