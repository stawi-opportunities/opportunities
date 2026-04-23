package service

import (
	"testing"

	eventsv1 "stawi.jobs/pkg/events/v1"
)

// noIcebergPersistence lists topics that are intentionally not persisted to
// Iceberg by the writer. Their payloads are authoritative elsewhere:
//
//   - TopicCanonicalsUpserted: canonical body lives at s3://…/jobs/<slug>.json (R2-direct)
//   - TopicCanonicalsExpired:  expiry is a Frame event only; no Iceberg table
//   - TopicTranslations:       translation body lives at s3://…/jobs/<slug>/<lang>.json
//
// The writer ACKs these events without writing an Iceberg record so that the
// materializer's Frame subscriber receives them without writer interference.
var noIcebergPersistence = map[string]bool{
	eventsv1.TopicCanonicalsUpserted: true,
	eventsv1.TopicCanonicalsExpired:  true,
	eventsv1.TopicTranslations:       true,
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
