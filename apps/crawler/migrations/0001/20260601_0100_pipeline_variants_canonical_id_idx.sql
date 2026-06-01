-- Direct index on pipeline_variants.canonical_id.
--
-- The worker's RecordErrorByCanonical / MarkPublishedByCanonical paths
-- (apps/worker/service/{publish,embed,translate}.go) update rows by
-- WHERE canonical_id = $1. Without this index that predicate fell back
-- to the partial hard_key index with a canonical_id Filter — an
-- index-scan-with-filter costing ~162k on the 4.5M-row hypertable. Under
-- the publish-failure path (R2 429s) those UPDATEs ran for MINUTES and
-- deadlocked (SQLSTATE 40P01), which slowed publish enough that NATS
-- redelivered the same CanonicalUpsertedV1, producing concurrent writes
-- to the same R2 object → "Reduce your concurrent request rate for the
-- same object" 429 storms → more failures. A direct partial index turns
-- the lookup into a sub-100-cost Index Only Scan, breaking the loop.
--
-- Partial (WHERE canonical_id IS NOT NULL) because canonical_id is only
-- set once a variant reaches the canonical stage; the index stays small.
-- NOTE: CREATE INDEX CONCURRENTLY is unsupported on TimescaleDB
-- hypertables, so this is a plain CREATE INDEX (brief write lock;
-- ~15s observed on the 3GB hypertable).
CREATE INDEX IF NOT EXISTS pipeline_variants_canonical_id_idx
    ON pipeline_variants (canonical_id)
    WHERE canonical_id IS NOT NULL;
