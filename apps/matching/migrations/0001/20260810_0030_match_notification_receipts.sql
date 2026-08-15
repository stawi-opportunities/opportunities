-- Receipts for match notification digests: one row per (candidate, match, channel)
-- so digests only include unseen opportunities (top-3 per send).

CREATE TABLE IF NOT EXISTS match_notification_receipts (
  candidate_id text NOT NULL,
  match_id text NOT NULL,
  opportunity_id text NOT NULL,
  channel text NOT NULL DEFAULT 'email',
  sent_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (candidate_id, match_id, channel)
);
CREATE INDEX IF NOT EXISTS match_notification_receipts_cand_sent_idx
  ON match_notification_receipts (candidate_id, sent_at DESC);
