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

func TestPartitionKeyRespectsSourceHint(t *testing.T) {
	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	pk := PartitionKey(TopicVariantsIngested, now, "greenhouse")
	if pk.DT != "2026-04-21" {
		t.Fatalf("DT=%q, want 2026-04-21", pk.DT)
	}
	if pk.Secondary != "greenhouse" {
		t.Fatalf("Secondary=%q, want greenhouse", pk.Secondary)
	}
}

func TestPartitionKeyCanonicalUsesClusterPrefix(t *testing.T) {
	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	pk := PartitionKey(TopicCanonicalsUpserted, now, "abcdef123456")
	if pk.Secondary != "ab" {
		t.Fatalf("Secondary=%q, want ab", pk.Secondary)
	}
}

func TestPartitionObjectPathVariantsLabel(t *testing.T) {
	pk := PartKey{DT: "2026-04-21", Secondary: "greenhouse"}
	got := pk.ObjectPath("variants", "abc123")
	want := "variants/dt=2026-04-21/src=greenhouse/abc123.parquet"
	if got != want {
		t.Fatalf("path=%q, want %q", got, want)
	}
}

func TestPartitionObjectPathCanonicalsLabel(t *testing.T) {
	pk := PartKey{DT: "2026-04-21", Secondary: "ab"}
	got := pk.ObjectPath("canonicals", "xyz789")
	want := "canonicals/dt=2026-04-21/cc=ab/xyz789.parquet"
	if got != want {
		t.Fatalf("path=%q, want %q", got, want)
	}
}
