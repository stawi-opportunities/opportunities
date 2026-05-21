package v1_test

import (
	"testing"

	v1 "github.com/stawi-opportunities/opportunities/apps/matching/service/matching/v1"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

func TestFanOutConsumer_NameIsCanonicalsUpsertedTopic(t *testing.T) {
	c := v1.NewFanOutConsumer(v1.FanOutConsumerDeps{})
	if got := c.Name(); got != eventsv1.TopicCanonicalsUpserted {
		t.Fatalf("Name() = %q, want %q", got, eventsv1.TopicCanonicalsUpserted)
	}
}
