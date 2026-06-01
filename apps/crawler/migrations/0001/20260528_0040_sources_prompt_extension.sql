-- Per-source extraction prompt extension. Appended after the kind's
-- base extraction prompt at extract time. Lets operators tune
-- extraction for tricky sources (sidebar widgets, custom HTML
-- attributes, multi-listing pages) without editing the shared kind
-- prompt that affects every source of that kind.
--
-- Empty by default (NOT NULL DEFAULT ''). 4 KB cap enforced by the
-- admin endpoint, not the schema.

ALTER TABLE sources
    ADD COLUMN IF NOT EXISTS extraction_prompt_extension TEXT NOT NULL DEFAULT '';
