-- Dedup safeguard on canonical_url_hash. Enqueue dedupes in
-- application code (UPSERT with priority-merge), but the
-- unique index ensures that even a racing concurrent insert
-- can never land two rows for the same canonical URL — the
-- second blows up with a unique-constraint violation that
-- Enqueue converts into a "duplicate, kept existing" result.

CREATE UNIQUE INDEX IF NOT EXISTS url_frontier_canonical_hash_uniq
    ON url_frontier (canonical_url_hash);
