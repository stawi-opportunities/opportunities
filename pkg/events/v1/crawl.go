package eventsv1

// CrawlRequestV1 is the control-plane event that tells a single
// crawler replica "please fetch this source (or URL within it) now."
// Emitted by the scheduler-tick admin endpoint (one per admitted
// source) and optionally chained by the crawl-request handler itself
// when a listing page exposes pagination.
//
// Wire format only — this event is not persisted in the Parquet log.
// apps/writer skips it in the encoder switch.
//
// Mode values:
//   - "auto":    use source.type's connector iterator (current default)
//   - "listing": fetch URL and run DiscoverLinks to fan out detail URLs
//   - "detail":  fetch URL, run Extract, emit one VariantIngestedV1
//
// Only "auto" is wired in Phase 4; "listing" and "detail" are reserved
// for a future per-page fan-out refactor that will land post-cutover.
type CrawlRequestV1 struct {
	// RequestID is a fresh xid per admission. Carried through
	// downstream events (page-completed) so audit logs can trace a
	// single crawl attempt end-to-end.
	RequestID string `json:"request_id" parquet:"request_id"`

	SourceID string `json:"source_id" parquet:"source_id"`

	// URL is optional. Empty means "start at source.base_url". Non-
	// empty is used by the not-yet-wired detail fan-out path.
	URL string `json:"url,omitempty" parquet:"url,optional"`

	// Cursor is the opaque pagination token emitted by the connector
	// on a prior page. Empty on first request for a source.
	Cursor string `json:"cursor,omitempty" parquet:"cursor,optional"`

	// Mode defaults to "auto". See the type docstring for values.
	Mode string `json:"mode,omitempty" parquet:"mode,optional"`

	// Attempt is 1 for a fresh admission; retries bump it. Logged,
	// not acted on — retry policy lives in Frame's redelivery config.
	Attempt int `json:"attempt,omitempty" parquet:"attempt,optional"`
}

// CrawlPageCompletedV1 is emitted by the crawl-request handler after
// it finishes processing one request. Self-consumed by the crawler's
// page-completed handler to update the Postgres sources row (cursor,
// next_crawl_at, health_score, quality counters). Persisted in the
// Parquet log for audit + replay.
type CrawlPageCompletedV1 struct {
	RequestID string `json:"request_id" parquet:"request_id"`
	SourceID  string `json:"source_id"  parquet:"source_id"`

	URL        string `json:"url,omitempty"         parquet:"url,optional"`
	HTTPStatus int    `json:"http_status,omitempty" parquet:"http_status,optional"`

	// JobsFound    — raw job count returned by the iterator
	// JobsEmitted  — count of jobs that passed quality gate and were emitted
	// JobsRejected — count of jobs that failed the deterministic quality check
	JobsFound    int `json:"jobs_found"     parquet:"jobs_found"`
	JobsEmitted  int `json:"jobs_emitted"   parquet:"jobs_emitted"`
	JobsRejected int `json:"jobs_rejected"  parquet:"jobs_rejected"`

	// Cursor is the connector's last-emitted pagination token. Empty
	// means "this source has no more pages to crawl this cycle."
	Cursor string `json:"cursor,omitempty" parquet:"cursor,optional"`

	// ErrorCode is populated when the crawl failed entirely (reachability
	// probe failed, connector raised an error, or the listing returned
	// a 5xx). Empty on success. ErrorMessage carries the human-readable
	// detail for audit.
	ErrorCode    string `json:"error_code,omitempty"    parquet:"error_code,optional"`
	ErrorMessage string `json:"error_message,omitempty" parquet:"error_message,optional"`
}

// SourceDiscoveredV1 is emitted by the crawl-request handler when a
// sampled DiscoverSites call finds a link to another job board on the
// current page. Self-consumed by the source-discovered handler to
// upsert the target URL as a `generic-html` source. Persisted.
//
// SourceID is the *origin* source — the crawl that discovered the new
// link. Kept so the source_expand audit trail shows provenance.
type SourceDiscoveredV1 struct {
	SourceID      string `json:"source_id"      parquet:"source_id"`
	DiscoveredURL string `json:"discovered_url" parquet:"discovered_url"`

	Name    string `json:"name,omitempty"    parquet:"name,optional"`
	Country string `json:"country,omitempty" parquet:"country,optional"`
	Type    string `json:"type,omitempty"    parquet:"type,optional"`
}
