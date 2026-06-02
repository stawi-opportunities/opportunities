-- Backfill host_state from the existing sources table. One row per
-- unique host so the URL frontier's Dequeue path always finds a
-- matching politeness row when a connector enqueues a URL.
--
-- The regex normalizes "https://example.com/path?q=1" → "example.com".
-- A handful of seed sources carry base_urls with ports (rare) or
-- raw IPs (rarer) — those land in host_state verbatim and admins
-- can tune the window/concurrency post-hoc. The HostOf helper in
-- pkg/frontier keys on the same lowercased+port-stripped form for
-- new enqueues, so existing-source URLs and freshly-enqueued URLs
-- find the same host_state row.
--
-- ON CONFLICT DO NOTHING keeps the migration idempotent — re-running
-- after operators have hand-tuned window_minutes / concurrency_max
-- on specific hosts must not clobber their values.

INSERT INTO host_state (host)
SELECT DISTINCT lower(regexp_replace(base_url, '^https?://([^/:]+).*', '\1'))
  FROM sources
 WHERE base_url IS NOT NULL
   AND base_url <> ''
ON CONFLICT (host) DO NOTHING;
