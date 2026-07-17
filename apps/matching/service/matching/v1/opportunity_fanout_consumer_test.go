package v1

import (
	"context"
	"encoding/json"
	"testing"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

func TestOpportunityFanOutConsumer_EmptyPayload(t *testing.T) {
	c := NewOpportunityFanOutConsumer(OpportunityFanOutConsumerDeps{})
	if err := c.Handle(context.Background(), nil, nil); err == nil {
		t.Fatal("expected error on empty payload")
	}
}

func TestOpportunityFanOutConsumer_MissingOpportunityID(t *testing.T) {
	c := NewOpportunityFanOutConsumer(OpportunityFanOutConsumerDeps{})
	env := eventsv1.NewEnvelope(eventsv1.SubjectOpportunityFanOut, eventsv1.OpportunityFanOutV1{
		Embedding: []float32{0.1, 0.2},
	})
	body, _ := json.Marshal(env)
	if err := c.Handle(context.Background(), nil, body); err == nil {
		t.Fatal("expected error when opportunity_id missing")
	}
}

func TestOpportunityFanOutConsumer_EmptyEmbeddingAcks(t *testing.T) {
	c := NewOpportunityFanOutConsumer(OpportunityFanOutConsumerDeps{})
	env := eventsv1.NewEnvelope(eventsv1.SubjectOpportunityFanOut, eventsv1.OpportunityFanOutV1{
		OpportunityID: "opp_1",
	})
	body, _ := json.Marshal(env)
	if err := c.Handle(context.Background(), nil, body); err != nil {
		t.Fatalf("empty embedding should ack (skip), got %v", err)
	}
}

func TestOpportunityFanOutConsumer_Name(t *testing.T) {
	c := NewOpportunityFanOutConsumer(OpportunityFanOutConsumerDeps{})
	if c.Name() != eventsv1.SubjectOpportunityFanOut {
		t.Fatalf("Name=%q", c.Name())
	}
}
