-- Conversation-grounded matching persona fields.
-- Single DO block: Neon/simple-protocol cannot multi-statement prepared SQL.

DO $mig$
BEGIN
  EXECUTE $q$
    ALTER TABLE candidate_placement_profiles
      ADD COLUMN IF NOT EXISTS conversation_digest text NOT NULL DEFAULT ''
  $q$;
  EXECUTE $q$
    ALTER TABLE candidate_match_indexes
      ADD COLUMN IF NOT EXISTS rerank_text text NOT NULL DEFAULT ''
  $q$;
END
$mig$;
