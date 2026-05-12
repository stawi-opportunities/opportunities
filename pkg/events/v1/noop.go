package eventsv1

import (
	"context"
	"encoding/json"
)

// NoopHandler is a Frame events.Handler that accepts a topic by name
// and silently ACKs every delivery. It exists so services that
// subscribe to a broad NATS subject (e.g. svc.opportunities.events.>)
// can suppress "event not found in registry" + retries-exhausted log
// spam for topics they receive but don't process. Use it in
// RegisterSubscriptions for every topic the service wants to ignore.
type NoopHandler struct{ Topic string }

func (h *NoopHandler) Name() string { return h.Topic }
func (h *NoopHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}
func (h *NoopHandler) Validate(context.Context, any) error { return nil }
func (h *NoopHandler) Execute(context.Context, any) error  { return nil }
