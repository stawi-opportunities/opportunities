package service

import (
	"testing"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

// The five pipeline topics every variant must traverse. embed/translate
// are subject-queue workers (not events-manager handlers) and are wired
// separately, so they are intentionally absent here.
// TopicVariantsNormalized is intentionally absent: as of the Events→Queue
// migration hop 1 the validate stage consumes it from a dedicated Frame
// Queue subject (SubjectPipelineNormalized) via ValidateWorker(), not from
// the events bus — so no events group handles it.
var allPipelineTopics = []string{
	eventsv1.TopicVariantsIngested,
	eventsv1.TopicVariantsValidated,
	eventsv1.TopicVariantsClustered,
	eventsv1.TopicCanonicalsUpserted,
}

// HandlersForGroup is a method on *Service (it constructs handlers from
// the service's deps). The constructors are plain struct literals and
// Name() returns a constant, so a zero-value Service is enough to assert
// the topic→group partitioning.
func newTestService() *Service { return &Service{} }

func TestHandlersForGroup_PartitionsAllTopicsExactlyOnce(t *testing.T) {
	s := newTestService()
	seen := map[string]int{}
	for _, g := range []string{"core", "validate", "publish"} {
		for _, h := range s.HandlersForGroup(g) {
			seen[h.Name()]++
		}
	}
	for _, topic := range allPipelineTopics {
		if seen[topic] != 1 {
			t.Errorf("topic %q handled by %d groups; want exactly 1", topic, seen[topic])
		}
	}
}

func TestHandlersForGroup_AllEqualsUnionOfGroups(t *testing.T) {
	s := newTestService()
	union := map[string]bool{}
	for _, g := range []string{"core", "validate", "publish"} {
		for _, h := range s.HandlersForGroup(g) {
			union[h.Name()] = true
		}
	}
	for _, h := range s.HandlersForGroup("all") {
		if !union[h.Name()] {
			t.Errorf("topic %q in group 'all' but in no split group", h.Name())
		}
	}
}

func TestHandlersForGroup_UnknownGroupIsEmpty(t *testing.T) {
	s := newTestService()
	if got := s.HandlersForGroup("nope"); len(got) != 0 {
		t.Errorf("unknown group returned %d handlers; want 0", len(got))
	}
}
