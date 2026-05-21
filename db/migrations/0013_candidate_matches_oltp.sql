-- 0013: New candidate_matches OLTP table (current state, mutable).
--
-- The pre-existing `candidate_matches` table was created by GORM
-- AutoMigrate against pkg/domain.CandidateMatch. It is retained
-- under the name `candidate_matches_legacy` so the legacy
-- match_runner.go / matches_weekly.go / preference_match.go paths
-- keep running unchanged. The legacy table is dropped in Phase 6
-- per spec §5.5 after the 30-day quiet window.
--
-- The new shape (spec §2.2):
--   match_id          PK
--   candidate_id      not null
--   opportunity_id    not null
--   UNIQUE (candidate_id, opportunity_id)
--   status            new|viewed|dismissed|applying|applied|overflow
--   score             retrieval/bi-encoder score
--   rerank_score      reranker score when present
--   viewed_at, applied_at  lifecycle timestamps
--   last_event_id     pointer back into candidate_match_events
--   created_at, updated_at

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_schema = 'public' AND table_name = 'candidate_matches'
    ) THEN
        EXECUTE 'ALTER TABLE candidate_matches RENAME TO candidate_matches_legacy';
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS candidate_matches (
    match_id        TEXT        PRIMARY KEY,
    candidate_id    TEXT        NOT NULL,
    opportunity_id  TEXT        NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'new',
    score           DOUBLE PRECISION NOT NULL,
    rerank_score    DOUBLE PRECISION,
    reranker_used   BOOLEAN     NOT NULL DEFAULT FALSE,
    viewed_at       TIMESTAMPTZ,
    applied_at      TIMESTAMPTZ,
    dismissed_at    TIMESTAMPTZ,
    last_event_id   TEXT,
    metadata        JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT candidate_matches_status_check CHECK (
        status IN ('new','viewed','dismissed','applying','applied','overflow')
    ),
    CONSTRAINT candidate_matches_pair_uniq UNIQUE (candidate_id, opportunity_id)
);

-- Hot read path: extension polls "matches for me, since cursor" ordered
-- by score then created_at — covers GET /api/me/matches in Phase 4.
CREATE INDEX IF NOT EXISTS candidate_matches_candidate_status_score_idx
    ON candidate_matches (candidate_id, status, score DESC, created_at DESC);

-- Cursor pagination on created_at (extension polls).
CREATE INDEX IF NOT EXISTS candidate_matches_candidate_created_idx
    ON candidate_matches (candidate_id, created_at DESC);

-- Reverse lookup (opportunity → all candidates matched).
CREATE INDEX IF NOT EXISTS candidate_matches_opportunity_idx
    ON candidate_matches (opportunity_id);
