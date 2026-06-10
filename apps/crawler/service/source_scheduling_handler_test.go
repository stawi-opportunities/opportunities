package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

// schedulingPayload builds the raw-envelope payload the handler's Execute
// decodes, matching what the queue delivers (an Envelope JSON blob).
func schedulingPayload(t *testing.T, sourceID string) *json.RawMessage {
	t.Helper()
	env := eventsv1.NewEnvelope(eventsv1.TopicSourceSchedulingChanged,
		eventsv1.SourceSchedulingChangedV1{SourceID: sourceID})
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	raw := json.RawMessage(b)
	return &raw
}

func TestSourceSchedulingHandler_ActiveSource_EnsuresSchedule(t *testing.T) {
	f := &fakeWorkflowClient{existing: map[string]string{}}
	getter := &fakeSourceGetter{rows: map[string]*domain.Source{
		"s1": src("s1", domain.SourceActive, 3600),
	}}
	h := NewSourceSchedulingHandler(f, getter, "http://crawler")

	if err := h.Execute(context.Background(), schedulingPayload(t, "s1")); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(f.created) != 1 || f.created[0] != workflowName("s1") {
		t.Fatalf("expected create/activate of %s, got created=%v archived=%v",
			workflowName("s1"), f.created, f.archived)
	}
}

func TestSourceSchedulingHandler_PausedSource_ArchivesSchedule(t *testing.T) {
	// A live (active) workflow exists; pausing the source must archive it.
	f := &fakeWorkflowClient{existing: map[string]string{workflowName("s1"): "wf_s1"}}
	getter := &fakeSourceGetter{rows: map[string]*domain.Source{
		"s1": src("s1", domain.SourcePaused, 3600),
	}}
	h := NewSourceSchedulingHandler(f, getter, "http://crawler")

	if err := h.Execute(context.Background(), schedulingPayload(t, "s1")); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(f.created) != 0 {
		t.Fatalf("paused source must not create a schedule, got %v", f.created)
	}
	if len(f.archived) != 1 || f.archived[0] != "wf_s1" {
		t.Fatalf("expected archive of wf_s1, got %v", f.archived)
	}
}

func TestSourceSchedulingHandler_MissingSource_ArchivesSchedule(t *testing.T) {
	// Source no longer exists (hard-deleted); any live schedule is archived.
	f := &fakeWorkflowClient{existing: map[string]string{workflowName("s1"): "wf_s1"}}
	getter := &fakeSourceGetter{rows: map[string]*domain.Source{}} // GetByID -> nil
	h := NewSourceSchedulingHandler(f, getter, "http://crawler")

	if err := h.Execute(context.Background(), schedulingPayload(t, "s1")); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(f.created) != 0 {
		t.Fatalf("missing source must not create a schedule, got %v", f.created)
	}
	if len(f.archived) != 1 || f.archived[0] != "wf_s1" {
		t.Fatalf("expected archive of wf_s1, got %v", f.archived)
	}
}

func TestSourceSchedulingHandler_Validate_RejectsEmpty(t *testing.T) {
	h := NewSourceSchedulingHandler(&fakeWorkflowClient{}, &fakeSourceGetter{}, "http://crawler")
	empty := json.RawMessage(nil)
	if err := h.Validate(context.Background(), &empty); err == nil {
		t.Fatal("expected error on empty payload")
	}
}
