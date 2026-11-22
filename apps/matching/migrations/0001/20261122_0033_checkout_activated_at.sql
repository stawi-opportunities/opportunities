-- Track whether a paid checkout's entitlement was granted, so replayed
-- webhooks are ignored and failed activations are retried.
-- GORM adds the column first; IF NOT EXISTS keeps this file standalone.
ALTER TABLE candidate_checkouts
  ADD COLUMN IF NOT EXISTS activated_at timestamptz;

-- Checkouts already paid before this change were activated by the old code
-- path. Backfill them so the reconciler does not re-grant historical
-- payments (that would revive expired or cancelled subscriptions).
UPDATE candidate_checkouts
SET activated_at = updated_at
WHERE status = 'paid' AND activated_at IS NULL;
