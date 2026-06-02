-- crawl_signals — per-source rolling 7-day signals used by the
-- freshness score. Materialized so the scheduler tick stays O(1)
-- rather than scanning crawl_jobs + pipeline_variants every cycle.
--
-- Refreshed by refresh_crawl_signals() (see _0072) on the
-- scheduler's tick; CONCURRENTLY so reads never block.
--
-- Column naming notes:
--   - pipeline_variants is a TimescaleDB hypertable keyed on
--     ingested_at (NOT discovered_at) and uses current_stage (NOT
--     state) for lifecycle. Accepted = current_stage = 'published';
--     rejected = current_stage = 'rejected'.
--   - crawl_jobs uses started_at; null when the job is still
--     scheduled, so the FILTER COUNT skips it.
--
-- Columns:
--   source_id              FK to sources.id (the materialized view's PK after refresh).
--   crawls_7d              number of crawl_jobs that ran in the last 7d.
--   variants_7d            total variants ingested (any current_stage).
--   accepted_7d            variants that reached current_stage='published'.
--   rejected_7d            variants explicitly rejected.
--   last_new_variant_at    NULL if no new variant in 7d; drives "cold" scoring.
CREATE MATERIALIZED VIEW IF NOT EXISTS crawl_signals AS
WITH per_source AS (
    SELECT
        cj.source_id,
        COUNT(*) FILTER (WHERE cj.started_at >= now() - interval '7 days')
            AS crawls_7d
    FROM crawl_jobs cj
    GROUP BY cj.source_id
),
variants AS (
    SELECT
        pv.source_id,
        COUNT(*) FILTER (WHERE pv.ingested_at >= now() - interval '7 days')
            AS variants_7d,
        COUNT(*) FILTER (WHERE pv.ingested_at >= now() - interval '7 days'
                          AND pv.current_stage = 'published')               AS accepted_7d,
        COUNT(*) FILTER (WHERE pv.ingested_at >= now() - interval '7 days'
                          AND pv.current_stage = 'rejected')                AS rejected_7d,
        MAX(pv.ingested_at) FILTER (WHERE pv.ingested_at >= now() - interval '7 days')
            AS last_new_variant_at
    FROM pipeline_variants pv
    GROUP BY pv.source_id
)
SELECT
    s.id                                AS source_id,
    COALESCE(p.crawls_7d, 0)            AS crawls_7d,
    COALESCE(v.variants_7d, 0)          AS variants_7d,
    COALESCE(v.accepted_7d, 0)          AS accepted_7d,
    COALESCE(v.rejected_7d, 0)          AS rejected_7d,
    v.last_new_variant_at               AS last_new_variant_at
FROM sources s
LEFT JOIN per_source p ON p.source_id = s.id
LEFT JOIN variants v   ON v.source_id = s.id;
