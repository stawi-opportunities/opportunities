-- 0015: continuous aggregates for the daily-cap fan-out filter and
--       the 24h engagement-counters dashboard.
--
-- candidate_match_events_daily: per (candidate_id, day) count of
-- 'generated' events. The fan-out path reads this to decide whether a
-- candidate is over their daily_cap; rows beyond cap go in as
-- status='overflow' instead of 'new' (spec §3.1 step 9).
--
-- engagement_events_hourly: per (candidate_id, opportunity_id, kind, hour)
-- count of beacons. Replaces the Valkey-counter + OpenObserve dual-
-- write pattern with a single Postgres query for the 24h view/apply
-- panels.
--
-- Both aggregates are bucket-only (no metadata payload), refresh on a
-- 5-minute policy, and follow the parent hypertables' retention so we
-- don't grow data older than the source data.

CREATE MATERIALIZED VIEW IF NOT EXISTS candidate_match_events_daily
WITH (timescaledb.continuous) AS
SELECT
    time_bucket(INTERVAL '1 day', occurred_at) AS day,
    candidate_id,
    count(*) FILTER (WHERE kind = 'generated') AS matches_generated,
    count(*) FILTER (WHERE kind = 'dismissed') AS matches_dismissed,
    count(*) FILTER (WHERE kind = 'overflow')  AS matches_overflowed
  FROM candidate_match_events
 GROUP BY day, candidate_id
WITH NO DATA;

SELECT add_continuous_aggregate_policy('candidate_match_events_daily',
    start_offset      => INTERVAL '7 days',
    end_offset        => INTERVAL '1 minute',
    schedule_interval => INTERVAL '5 minutes',
    if_not_exists     => TRUE);

CREATE MATERIALIZED VIEW IF NOT EXISTS engagement_events_hourly
WITH (timescaledb.continuous) AS
SELECT
    time_bucket(INTERVAL '1 hour', occurred_at) AS hour,
    candidate_id,
    opportunity_id,
    kind,
    count(*) AS event_count
  FROM engagement_events
 GROUP BY hour, candidate_id, opportunity_id, kind
WITH NO DATA;

SELECT add_continuous_aggregate_policy('engagement_events_hourly',
    start_offset      => INTERVAL '2 days',
    end_offset        => INTERVAL '1 minute',
    schedule_interval => INTERVAL '5 minutes',
    if_not_exists     => TRUE);
