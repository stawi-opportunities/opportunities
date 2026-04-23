package service

import (
	"testing"

	eventsv1 "stawi.jobs/pkg/events/v1"
)

// TestAllTopicsHaveDispatch verifies that every topic returned by
// eventsv1.AllTopics() has a registered batchDispatch entry.
// This is a compile-time guard against adding a new topic to AllTopics
// without wiring it into batchDispatch — a silent data-loss regression.
func TestAllTopicsHaveDispatch(t *testing.T) {
	for _, topic := range eventsv1.AllTopics() {
		ident, builder := batchDispatch(topic)
		if ident == nil || builder == nil {
			t.Errorf("topic %q has no batchDispatch entry", topic)
		}
	}
}
