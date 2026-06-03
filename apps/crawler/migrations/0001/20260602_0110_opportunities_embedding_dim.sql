-- Reconcile opportunities.embedding to the deployed embedding model's
-- native dimension (1024 for intfloat/multilingual-e5-large).
--
-- Why this exists: the column and the deployed model drifted apart.
-- pgvector rejects any dimension mismatch on every write ("expected N
-- dimensions, not M", SQLSTATE 22000), the materializer's UpdateEmbedding
-- soft-fails it, and opportunities.embedding silently stays NULL for the
-- whole corpus — starving both the reverse-KNN (candidate CV ->
-- opportunities) and the forward fan-out. (Observed: the column was
-- vector(1024) while a 384-dim e5-small was briefly deployed; 100% of
-- embeddings dropped.)
--
-- The embedding dimension is a property of the model architecture: e5-small
-- emits only 384, e5-large/bge-m3 emit 1024. The model choice drives this
-- value; EMBEDDING_DIM and the deployed EMBEDDING_MODEL must agree with it.
-- The boot-time guard in the materializer (EMBEDDING_DIM vs the live column
-- typmod) fails loudly if schema and model ever drift again.
--
-- Idempotent + safe: no-op when already vector(1024); when the dimension
-- differs the column is retyped via USING NULL. A differing dimension
-- means any stored vectors are unusable by the configured model anyway
-- (they must be re-embedded), so clearing them is correct, not lossy —
-- the embedding backfill / steady-state embed path re-derives them.
DO $$
DECLARE
    target_dim CONSTANT int := 1024;
    cur_dim    int;
BEGIN
    IF to_regclass('public.opportunities') IS NULL THEN
        RAISE NOTICE 'opportunities table absent; skipping embedding dim reconcile';
        RETURN;
    END IF;

    SELECT a.atttypmod - 4 INTO cur_dim
    FROM pg_attribute a
    JOIN pg_class c ON c.oid = a.attrelid
    WHERE c.relname = 'opportunities'
      AND a.attname = 'embedding'
      AND NOT a.attisdropped;

    IF cur_dim IS NULL THEN
        RAISE NOTICE 'opportunities.embedding column absent; skipping';
        RETURN;
    END IF;

    IF cur_dim = target_dim THEN
        RETURN; -- already aligned; nothing to do
    END IF;

    RAISE NOTICE 'reconciling opportunities.embedding from vector(%) to vector(%)', cur_dim, target_dim;
    DROP INDEX IF EXISTS opportunities_embedding_diskann_idx;
    EXECUTE format(
        'ALTER TABLE opportunities ALTER COLUMN embedding TYPE vector(%s) USING NULL::vector(%s)',
        target_dim, target_dim);
    CREATE INDEX opportunities_embedding_diskann_idx
        ON opportunities USING diskann (embedding);
END $$;
