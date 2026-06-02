package service

import (
	"testing"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

// noIcebergPersistence lists topics that are intentionally not persisted to
// Iceberg by the writer. Their payloads are authoritative elsewhere:
//
//   - TopicCanonicalsUpserted:    canonical body lives at s3://…/jobs/<slug>.json (R2-direct)
//   - TopicCanonicalsExpired:     expiry is a Frame event only; no Iceberg table
//   - TopicTranslations:          translation body lives at s3://…/jobs/<slug>/<lang>.json
//   - TopicOpportunityAutoFlagged: user-driven moderation signal — the
//     materializer subscribes to it to hide rows from search; the
//     authoritative record of the underlying user flag lives in
//     opportunity_flags (Postgres).
//   - TopicDefinitionsChanged: pure control-plane signal — every Loader
//     subscribes to call Invalidate on the matching cache entry. The
//     authoritative state of every definition lives in R2 already, so
//     persisting the change event to Iceberg adds no audit value.
//
// The writer ACKs these events without writing an Iceberg record so that the
// materializer's Frame subscriber receives them without writer interference.
var noIcebergPersistence = map[string]bool{
	eventsv1.TopicCanonicalsUpserted:     true,
	eventsv1.TopicCanonicalsExpired:      true,
	eventsv1.TopicTranslations:           true,
	eventsv1.TopicOpportunityAutoFlagged: true,
	eventsv1.TopicDefinitionsChanged:     true,

	// TopicApplicationSubmitted: needs an Iceberg table + Arrow
	// schema + builder before it can leave this list. The auto-apply
	// service emits it, the candidate_applications Postgres row is
	// the authoritative store for now; analytics rollup over Iceberg
	// is a follow-up. Listing it here keeps the writer from panicking
	// the dispatch test and acks the message harmlessly.
	eventsv1.TopicApplicationSubmitted: true,

	// Session-capture events: the authoritative store is the
	// candidate_sessions Postgres table + the notification service
	// downstream of TopicSessionRequired/Expired. An analytics rollup
	// over these events is a Phase 8+ follow-up — until then they ack
	// without writing to Iceberg.
	eventsv1.TopicSessionCaptured: true,
	eventsv1.TopicSessionRequired: true,
	eventsv1.TopicSessionExpired:  true,
}

// TestAllTopicsHaveDispatch verifies that every topic returned by
// eventsv1.AllTopics() has a registered batchDispatch entry OR is explicitly
// listed in noIcebergPersistence (meaning it is ACKed without Iceberg write).
// This guards against accidentally adding a new topic without wiring it.
func TestAllTopicsHaveDispatch(t *testing.T) {
	for _, topic := range eventsv1.AllTopics() {
		if noIcebergPersistence[topic] {
			continue // intentionally no Iceberg persistence
		}
		ident, builder := batchDispatch(topic)
		if ident == nil || builder == nil {
			t.Errorf("topic %q has no batchDispatch entry", topic)
		}
	}
}
