package eventsv1

// SourceSchedulingChangedV1 is the control-plane signal that a source's
// crawl-scheduling state may have changed (created, approved, paused,
// resumed, stopped, started, deleted, or its crawl_interval_sec edited).
// It carries only the source id — the consuming crawler re-derives the
// desired schedule from the source's live status, which makes the handler
// idempotent and order-independent.
type SourceSchedulingChangedV1 struct {
	SourceID string `json:"source_id"`
}
