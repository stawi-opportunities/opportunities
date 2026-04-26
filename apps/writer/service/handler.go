package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

// WriterHandler implements frame's event handler interface for a
// single topic. Each registered topic gets its own handler instance
// bound to the same Buffer so all events land in the same sharded
// map. Handlers are pure routers — they derive the partition hint
// from the payload and call Buffer.Add. All actual IO (Parquet,
// R2) happens out-of-band in the flusher.
type WriterHandler struct {
	topic  string
	buffer *Buffer
}

// NewWriterHandler binds a handler to a topic + buffer.
func NewWriterHandler(topic string, buffer *Buffer) *WriterHandler {
	return &WriterHandler{topic: topic, buffer: buffer}
}

// Name — the event name Frame dispatches on.
func (h *WriterHandler) Name() string { return h.topic }

// PayloadType returns a pointer to a json.RawMessage so Frame skips
// payload-specific deserialization. The Buffer re-reads the raw bytes
// to peek at event_type + occurred_at + partition hint; the writer's
// flusher also reads raw bytes and typed-decodes per collection.
func (h *WriterHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

// Validate — a cheap shape check so poisoned events dead-letter early.
func (h *WriterHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("writer: empty or wrong-type payload")
	}
	return nil
}

// Execute enqueues the event into the buffer. A returned error tells
// Frame to negative-ack (redeliver). A nil error tells Frame to ack —
// but ack-after-upload semantics require Frame's at-least-once mode.
// In Phase 1 we accept at-least-once with an in-process buffer; the
// flusher promotes acks to ack-after-upload in Phase 2 when durable
// flush tracking lands.
func (h *WriterHandler) Execute(ctx context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil {
		return errors.New("writer: wrong payload type")
	}
	hint := extractHint(*raw, h.topic)
	if _, err := h.buffer.Add(json.RawMessage(*raw), hint); err != nil {
		return fmt.Errorf("writer: buffer.Add: %w", err)
	}
	return nil
}

// extractHint pulls the partition-secondary value from the raw
// envelope JSON. For Phase 1 we only wire VariantIngestedV1 (hint =
// source_id). Additional types join this switch as they come online.
func extractHint(raw json.RawMessage, topic string) string {
	switch topic {
	case eventsv1.TopicVariantsIngested,
		eventsv1.TopicCrawlPageCompleted,
		eventsv1.TopicSourcesDiscovered:
		return decodeField(raw, "payload.source_id")
	case eventsv1.TopicCanonicalsUpserted, eventsv1.TopicCanonicalsExpired:
		return decodeField(raw, "payload.cluster_id")
	case eventsv1.TopicEmbeddings:
		return decodeField(raw, "payload.canonical_id")
	case eventsv1.TopicTranslations:
		return decodeField(raw, "payload.lang")
	case eventsv1.TopicCVUploaded,
		eventsv1.TopicCVExtracted,
		eventsv1.TopicCVImproved,
		eventsv1.TopicCandidateEmbedding,
		eventsv1.TopicCandidatePreferencesUpdated,
		eventsv1.TopicCandidateMatchesReady:
		return decodeField(raw, "payload.candidate_id")
	default:
		return ""
	}
}

// decodeField walks a dotted path through the JSON tree and returns
// the string value (or "" on any miss).
func decodeField(raw json.RawMessage, dotted string) string {
	// Two-step descent: first unmarshal the envelope, then descend into
	// the payload sub-object. Both levels are decoded as
	// map[string]json.RawMessage so that non-string fields in the
	// payload (e.g. salary_min, salary_max) don't break the decode.
	var step1 map[string]json.RawMessage
	if err := json.Unmarshal(raw, &step1); err != nil {
		return ""
	}
	// dotted is "payload.key"; split on first '.'
	for i := 0; i < len(dotted); i++ {
		if dotted[i] == '.' {
			head := dotted[:i]
			tail := dotted[i+1:]
			sub, ok := step1[head]
			if !ok {
				return ""
			}
			var m map[string]json.RawMessage
			if err := json.Unmarshal(sub, &m); err != nil {
				return ""
			}
			valRaw, ok := m[tail]
			if !ok {
				return ""
			}
			var s string
			if err := json.Unmarshal(valRaw, &s); err != nil {
				return ""
			}
			return s
		}
	}
	return ""
}
