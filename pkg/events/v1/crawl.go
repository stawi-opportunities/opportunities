package eventsv1

import "time"

// CrawlRequestV1 is the control-plane event that tells a single
// crawler replica "please fetch this source (or URL within it) now."
// Emitted by the scheduler-tick admin endpoint (one per admitted
// source) and optionally chained by the crawl-request handler itself
// when a listing page exposes pagination.
//
// Wire format for crawl control.
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
	RequestID string `json:"request_id"`

	SourceID string `json:"source_id"`

	// IdempotencyKey is the (source_id, tick_minute) tuple the
	// scheduler stamps so the crawl handler can dedupe NATS
	// at-least-once redeliveries: redeliveries carry the same key
	// and reuse the same crawl_jobs row instead of inserting a
	// duplicate. Empty key means the handler derives one from
	// SourceID + ScheduledAt at row-open time.
	IdempotencyKey string `json:"idempotency_key,omitempty"`

	// ScheduledAt is the tick time the scheduler intended this
	// request for. Carried explicitly so the audit row's
	// scheduled_at column reflects the planned tick, not whatever
	// `now()` happens to be at handler dispatch (which can drift
	// under NATS lag or redelivery).
	ScheduledAt time.Time `json:"scheduled_at,omitempty"`

	// URL is optional. Empty means "start at source.base_url". Non-
	// empty is used by the not-yet-wired detail fan-out path.
	URL string `json:"url,omitempty"`

	// Cursor is the opaque pagination token emitted by the connector
	// on a prior page. Empty on first request for a source.
	Cursor string `json:"cursor,omitempty"`

	// Mode defaults to "auto". See the type docstring for values.
	Mode string `json:"mode,omitempty"`

	// Attempt is 1 for a fresh admission; retries bump it. Logged,
	// not acted on — retry policy lives in Frame's redelivery config.
	Attempt int `json:"attempt,omitempty"`

	// RunID ties this request to a crawl_runs row. Set on a continuation
	// (the handler self-re-enqueues after a bounded slice) and on a
	// watchdog re-drive, so the handler resumes that exact run from its
	// persisted cursor rather than starting a fresh pass. Empty on a
	// scheduler-originated request, where the handler opens (or single-
	// flight-drops to) the source's active run.
	RunID string `json:"run_id,omitempty"`

	// IsContinuation marks a self-re-enqueued slice (vs a fresh scheduled
	// start or a watchdog re-drive). Informational for tracing/telemetry.
	IsContinuation bool `json:"is_continuation,omitempty"`
}

// CrawlPageCompletedV1 is emitted by the crawl-request handler after
// it finishes processing one request. Self-consumed by the crawler's
// page-completed handler to update the Postgres sources row (cursor,
// next_crawl_at, health_score, quality counters). Persisted in the
// event ledger log for audit + replay.
type CrawlPageCompletedV1 struct {
	RequestID string `json:"request_id"`
	SourceID  string `json:"source_id" `

	URL        string `json:"url,omitempty"        `
	HTTPStatus int    `json:"http_status,omitempty"`

	// JobsFound    — raw job count returned by the iterator
	// JobsEmitted  — count of jobs that passed quality gate and were emitted
	// JobsRejected — count of jobs that failed the deterministic quality check
	JobsFound    int `json:"jobs_found"    `
	JobsEmitted  int `json:"jobs_emitted"  `
	JobsRejected int `json:"jobs_rejected" `

	// Cursor is the connector's last-emitted pagination token. Empty
	// means "this source has no more pages to crawl this cycle."
	Cursor string `json:"cursor,omitempty"`

	// ErrorCode is populated when the crawl failed entirely (reachability
	// probe failed, connector raised an error, or the listing returned
	// a 5xx). Empty on success. ErrorMessage carries the human-readable
	// detail for audit.
	ErrorCode    string `json:"error_code,omitempty"   `
	ErrorMessage string `json:"error_message,omitempty"`

	EventID    string    `json:"-"`
	OccurredAt time.Time `json:"-"`
}

// SourceDiscoveredV1 is emitted by the crawl-request handler when a
// sampled DiscoverSites call finds a link to another job board on the
// current page. Self-consumed by the source-discovered handler to
// upsert the target URL as a `generic-html` source. Persisted.
//
// SourceID is the *origin* source — the crawl that discovered the new
// link. Kept so the source_expand audit trail shows provenance.
type SourceDiscoveredV1 struct {
	SourceID      string `json:"source_id"     `
	DiscoveredURL string `json:"discovered_url"`

	Name    string `json:"name,omitempty"   `
	Country string `json:"country,omitempty"`
	Type    string `json:"type,omitempty"   `

	EventID    string    `json:"-"`
	OccurredAt time.Time `json:"-"`
}
