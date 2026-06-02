CREATE INDEX IF NOT EXISTS pipeline_variants_crawl_job_idx
    ON pipeline_variants (crawl_job_id)
    WHERE crawl_job_id IS NOT NULL;
