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
