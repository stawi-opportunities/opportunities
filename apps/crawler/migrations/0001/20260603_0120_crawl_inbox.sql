-- crawl_inbox: a durable Postgres buffer between crawling and the NATS
-- pipeline, so a crawl burst (one paginated job board emits hundreds–thousands
-- of variants at once) doesn't slam JetStream all at once.
--
-- The crawler/frontier INSERT each freshly-crawled VariantIngestedV1 here
-- instead of publishing straight to pl_ingested. A rate-limited pump drains
-- the inbox into pl_ingested only while the queue is below its high-water mark,
-- then DELETEs the drained rows. The DB absorbs bursts; NATS only ever holds
-- the working set, which is what kept the pipeline (every stage blocks on a
-- JetStream fsync ack) from collapsing under the per-source crawl flood.
--
-- This is a transient work buffer, NOT a ledger — rows are deleted once pumped,
-- so it stays small. claimed_at lets the pump use FOR UPDATE SKIP LOCKED across
-- replicas and re-claim rows orphaned by a pump crash (claimed_at older than a
-- timeout). Idempotent: safe to re-run.
CREATE TABLE IF NOT EXISTS crawl_inbox (
    variant_id  text PRIMARY KEY,
    source_id   text NOT NULL,
    payload     jsonb NOT NULL,          -- the VariantIngestedV1 envelope, verbatim
    created_at  timestamptz NOT NULL DEFAULT now(),
    claimed_at  timestamptz              -- NULL = unclaimed; set when a pump claims it
);

-- Pump claim order: oldest unclaimed first. Partial index keeps it tiny (only
-- the in-flight buffer, not the whole history).
CREATE INDEX IF NOT EXISTS idx_crawl_inbox_unclaimed
    ON crawl_inbox (created_at)
    WHERE claimed_at IS NULL;

-- Re-claim sweep: find rows whose claim went stale (pump died mid-batch).
CREATE INDEX IF NOT EXISTS idx_crawl_inbox_claimed
    ON crawl_inbox (claimed_at)
    WHERE claimed_at IS NOT NULL;
