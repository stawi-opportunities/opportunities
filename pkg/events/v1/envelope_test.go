package eventsv1

import (
	"encoding/json"
	"testing"
	"time"
)

type testPayload struct {
	Value string `json:"value"`
}

func TestNewEnvelopeStampsIDsAndTimestamps(t *testing.T) {
	env := NewEnvelope("test.topic.v1", testPayload{Value: "x"})

	if env.EventID == "" {
		t.Fatal("event_id must be set by NewEnvelope")
	}
	if env.EventType != "test.topic.v1" {
		t.Fatalf("event_type=%q, want test.topic.v1", env.EventType)
	}
	if time.Since(env.OccurredAt) > time.Second {
		t.Fatalf("occurred_at=%v too old", env.OccurredAt)
	}
	if env.SchemaVersion != 1 {
		t.Fatalf("schema_version=%d, want 1", env.SchemaVersion)
	}
}

func TestEnvelopeJSONRoundTrip(t *testing.T) {
	orig := NewEnvelope("test.topic.v1", testPayload{Value: "roundtrip"})
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Envelope[testPayload]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.EventID != orig.EventID {
		t.Fatalf("event_id lost on round-trip: got %q want %q", back.EventID, orig.EventID)
	}
	if back.Payload.Value != "roundtrip" {
		t.Fatalf("payload lost: %+v", back.Payload)
	}
}
