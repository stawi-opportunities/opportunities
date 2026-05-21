package v1_test

import (
	"testing"

	v1 "github.com/stawi-opportunities/opportunities/apps/matching/service/matching/v1"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

func TestCandidateChangeConsumer_NameMatchesTopic(t *testing.T) {
	c := v1.NewCandidateChangeConsumer(v1.CandidateChangeConsumerDeps{
		Topic: eventsv1.TopicCandidatePreferencesUpdated,
	})
	if c.Name() != eventsv1.TopicCandidatePreferencesUpdated {
		t.Fatalf("expected Name=%q, got %q", eventsv1.TopicCandidatePreferencesUpdated, c.Name())
	}
}
