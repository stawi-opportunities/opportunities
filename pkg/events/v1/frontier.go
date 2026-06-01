package eventsv1

import "time"

// TopicURLEnqueued is the wake-up signal emitted by frontier.Enqueue
// after a URL row lands in url_frontier. The frontier-worker subscribes
// and pulls the next eligible batch from Postgres — the event is a
// nudge, not the work payload. The queue stays the source of truth.
const TopicURLEnqueued = "crawl.url.enqueued.v1"

// URLEnqueuedV1 is the payload of the wake-up event. Slimmer than a
// full URL row because the worker re-reads from Postgres anyway; we
// only need enough to make dashboards and DLQ traces meaningful.
type URLEnqueuedV1 struct {
	URLID        string    `json:"url_id"        parquet:"url_id"`
	CanonicalURL string    `json:"canonical_url" parquet:"canonical_url"`
	Host         string    `json:"host"          parquet:"host"`
	SourceID     string    `json:"source_id"     parquet:"source_id"`
	Priority     float64   `json:"priority"      parquet:"priority"`
	DiscoveredAt time.Time `json:"discovered_at" parquet:"discovered_at"`

	EventID    string    `json:"-" parquet:"event_id"`
	OccurredAt time.Time `json:"-" parquet:"occurred_at"`
}
