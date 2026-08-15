-- Speeds paginated dashboard matches feed:
--   WHERE candidate_id = $1 AND status NOT IN ('overflow','dismissed')
--   ORDER BY COALESCE(rerank_score, score) DESC, created_at DESC, opportunity_id
-- Expression index on effective score; partial to active rows only.

CREATE INDEX IF NOT EXISTS candidate_matches_active_score_idx
  ON candidate_matches (
    candidate_id,
    (COALESCE(rerank_score, score)) DESC,
    created_at DESC,
    opportunity_id ASC
  )
  WHERE status NOT IN ('overflow', 'dismissed');
