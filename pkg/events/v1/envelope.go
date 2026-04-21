// Package eventsv1 defines versioned event payloads shared by every
// service that publishes or consumes the job-ingestion event log.
//
// Every event on the bus wraps a payload in an Envelope. Consumers
// generic-deserialize the outer Envelope fields, then type-assert the
// Payload against the event_type string to dispatch. Parquet writes
// flatten Envelope + Payload into a single row per event type.
package eventsv1

import (
	"time"

	"github.com/rs/xid"
)

// Envelope is the common wrapper carried on every event on the bus.
// The Payload type parameter is the event-specific body (e.g.
// VariantIngestedV1). Keeping the envelope generic means both
// producers and the writer can use compile-time typed access instead
// of map[string]any dispatch.
type Envelope[P any] struct {
	EventID       string    `json:"event_id"`
	EventType     string    `json:"event_type"`
	OccurredAt    time.Time `json:"occurred_at"`
	SchemaVersion uint16    `json:"schema_version"`
	TraceID       string    `json:"trace_id,omitempty"`
	Payload       P         `json:"payload"`
}

// NewEnvelope constructs an Envelope with a fresh xid, UTC timestamp,
// and schema_version=1. Callers override schema_version only when
// introducing an additive schema change (breaking changes bump the
// .vN suffix in event_type instead).
func NewEnvelope[P any](eventType string, payload P) Envelope[P] {
	return Envelope[P]{
		EventID:       xid.New().String(),
		EventType:     eventType,
		OccurredAt:    time.Now().UTC(),
		SchemaVersion: 1,
		Payload:       payload,
	}
}
