-- Opt-in flag that selects the URL-frontier path for a source.
-- Default false: every existing source keeps its current
-- per-source iterator behaviour with zero operator action.
-- Admins flip the column via PATCH /admin/sources/{id} after
-- they've verified the frontier behaves well for the source.

ALTER TABLE sources
    ADD COLUMN IF NOT EXISTS frontier_enabled BOOLEAN NOT NULL DEFAULT FALSE;
