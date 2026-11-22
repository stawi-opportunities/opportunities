-- Crawl-plane retention (see pkg/repository/retention.go).
--
-- job_ingest_events stays append-only for normal writers. Its triggers now
-- use a dedicated guard that lets exactly one thing through: a DELETE of a
-- row older than 90 days, issued in a transaction that set
-- stawi.retention_sweep = 'on' (SET LOCAL by the crawler's retention sweep).
-- UPDATE, TRUNCATE and any other DELETE still raise. The shared
-- append_only_guard() (also used by matching's ledgers) is unchanged.
--
-- Locking: CREATE OR REPLACE TRIGGER (PG14+; Neon runs 16) swaps the
-- function without DROP TRIGGER's ACCESS EXCLUSIVE lock, and no index is
-- built here (the sweep bounds its scans with the time-ordered xid primary
-- keys). lock_timeout makes the setup Job fail fast and retry instead of
-- queueing live writers behind it.

SET LOCAL lock_timeout = '10s';

CREATE OR REPLACE FUNCTION ingest_events_guard() RETURNS trigger AS $body$
BEGIN
    IF TG_OP = 'DELETE' AND current_setting('stawi.retention_sweep', true) = 'on' THEN
        -- 90 days as a fixed number of seconds, the unit the sweep uses
        -- (a calendar interval shifts by an hour across DST time zones).
        IF OLD.occurred_at < now() - interval '7776000 seconds' THEN
            RETURN OLD;
        END IF;
    END IF;
    RAISE EXCEPTION '% is append-only: % is not allowed', TG_TABLE_NAME, TG_OP;
END;
$body$ LANGUAGE plpgsql;

CREATE OR REPLACE TRIGGER job_ingest_events_append_only
    BEFORE UPDATE OR DELETE ON job_ingest_events
    FOR EACH ROW EXECUTE FUNCTION ingest_events_guard();
CREATE OR REPLACE TRIGGER job_ingest_events_no_truncate
    BEFORE TRUNCATE ON job_ingest_events
    FOR EACH STATEMENT EXECUTE FUNCTION ingest_events_guard();
