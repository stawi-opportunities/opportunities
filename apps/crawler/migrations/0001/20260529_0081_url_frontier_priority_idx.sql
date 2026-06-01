-- Dequeue hot-path index. The frontier-worker pulls the next
-- batch with:
--
--   SELECT ... FROM url_frontier f
--     JOIN host_state h ON h.host = f.host
--    WHERE f.state = 'pending'
--      AND (f.next_attempt_at IS NULL OR f.next_attempt_at <= now())
--      AND h.next_eligible_at <= now()
--    ORDER BY f.priority DESC, f.enqueued_at ASC
--    LIMIT $1 FOR UPDATE OF f SKIP LOCKED;
--
-- Composite (state, priority DESC, enqueued_at) lets the
-- planner skip in_flight/done/failed rows and stream the
-- highest-priority pending rows in enqueued_at order without
-- a sort.

CREATE INDEX IF NOT EXISTS url_frontier_dequeue_idx
    ON url_frontier (state, priority DESC, enqueued_at ASC);
