package connectors

import (
	"context"
	"encoding/json"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// CheckpointState is the per-page state a connector emits to drive
// resumption. The Cursor is the connector's own serialized state
// (opaque to the crawl handler); PageIdx is informational + drives
// the page counter the operator sees on the trace UI.
type CheckpointState struct {
	Cursor  json.RawMessage `json:"cursor"`
	PageIdx int             `json:"page_idx"`
	LastURL string          `json:"last_url,omitempty"`
}

// CheckpointableIterator is the optional extension a CrawlIterator
// implements to participate in checkpoint persistence. The handler
// type-asserts on this; iterators that don't implement it simply
// don't checkpoint and the source crawls from page 1 on every
// redelivery (current behaviour pre-D1).
type CheckpointableIterator interface {
	Checkpoint() *CheckpointState
}

// ResumableConnector is the optional extension a Connector implements
// to accept a previous checkpoint and resume from it. The handler
// type-asserts on this; connectors that don't implement it ignore
// the checkpoint and start fresh.
//
// The checkpoint may be nil (no prior state) — connectors that DO
// implement this interface should treat a nil cp as "start from
// scratch", identical to the non-resumable Crawl path. The handler
// only invokes CrawlResume when it has a non-stale checkpoint to
// pass in, so connectors don't need to filter stale themselves.
type ResumableConnector interface {
	CrawlResume(ctx context.Context, src domain.Source, cp *CheckpointState) CrawlIterator
}
