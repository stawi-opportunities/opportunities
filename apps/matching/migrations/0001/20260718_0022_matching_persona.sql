-- Conversation-grounded matching persona fields.
-- conversation_digest: rolling intent from AI chat (not full transcript).
-- rerank_text: short denormalized persona for Path A cross-encoder stage.

ALTER TABLE candidate_placement_profiles
    ADD COLUMN IF NOT EXISTS conversation_digest text NOT NULL DEFAULT '';

ALTER TABLE candidate_match_indexes
    ADD COLUMN IF NOT EXISTS rerank_text text NOT NULL DEFAULT '';
