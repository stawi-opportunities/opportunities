-- Activation tracking for paid checkouts: replayed webhooks are ignored,
-- concurrent activations are serialised by a claim, failed activations are
-- retried with backoff, and "newer purchase" checks use payment time.
-- GORM adds these columns first; IF NOT EXISTS keeps this file standalone.
ALTER TABLE candidate_checkouts
  ADD COLUMN IF NOT EXISTS activated_at timestamptz,
  ADD COLUMN IF NOT EXISTS activation_claimed_at timestamptz,
  ADD COLUMN IF NOT EXISTS activation_attempts integer NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS next_activation_at timestamptz,
  ADD COLUMN IF NOT EXISTS activation_error text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS paid_at timestamptz,
  ADD COLUMN IF NOT EXISTS activation_outcome text NOT NULL DEFAULT '';

-- Checkouts already paid before this change were activated by the old code
-- path. Backfill them so the reconciler does not re-grant historical
-- payments (that would revive expired or cancelled subscriptions).
-- updated_at is the best available approximation of the payment time.
UPDATE candidate_checkouts
SET activated_at = updated_at,
    paid_at = COALESCE(paid_at, updated_at),
    activation_outcome = 'applied'
WHERE status = 'paid' AND activated_at IS NULL;
