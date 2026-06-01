-- Per-host politeness state shared across every connector that
-- enqueues URLs into url_frontier. The Dequeue path joins on
-- host_state so the per-host window + concurrency cap are
-- enforced at the queue level — discovery connectors don't
-- have to think about politeness.
--
-- window_minutes: minimum gap between fetches against this
--   host. Default 1 minute is intentionally polite; admins
--   can lower it for hosts that explicitly tolerate higher
--   rates (e.g. self-hosted ATS endpoints).
-- next_eligible_at: last_request_at + window_minutes; computed
--   on every successful Dequeue claim. NULL on a fresh host
--   (eligible immediately).
-- concurrency_max / concurrency_now: lets ops bound how many
--   in-flight URLs the frontier may hold against one host.
--   Default 1 (strictly serial); raise for hosts that scale.
-- ok_count_24h / err_count_24h: rolling counters used by D3 to
--   tune source health when a host degrades. Updated by
--   Complete / Fail.

CREATE TABLE IF NOT EXISTS host_state (
    host             TEXT         PRIMARY KEY,
    window_minutes   INTEGER      NOT NULL DEFAULT 1,
    last_request_at  TIMESTAMPTZ,
    next_eligible_at TIMESTAMPTZ,
    ok_count_24h     INTEGER      NOT NULL DEFAULT 0,
    err_count_24h    INTEGER      NOT NULL DEFAULT 0,
    concurrency_max  INTEGER      NOT NULL DEFAULT 1,
    concurrency_now  INTEGER      NOT NULL DEFAULT 0
);
