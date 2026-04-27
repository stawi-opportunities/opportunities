package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/telemetry"
)

// classifyEmbedFailure maps an error from the embed provider to a
// short, low-cardinality reason tag suitable for an OTel attribute.
func classifyEmbedFailure(err error) string {
	if err == nil {
		return "unknown"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "rate limit"), strings.Contains(msg, "429"):
		return "rate_limit"
	case strings.Contains(msg, "parse"), strings.Contains(msg, "decode"), strings.Contains(msg, "unmarshal"):
		return "parse"
	case strings.Contains(msg, "context deadline"), strings.Contains(msg, "timeout"):
		return "timeout"
	case strings.Contains(msg, "connection"), strings.Contains(msg, "network"), strings.Contains(msg, "no such host"):
		return "network"
	default:
		return "unknown"
	}
}

// EmbedHandler consumes CanonicalUpsertedV1 and emits EmbeddingV1.
// If no embedder is configured, it emits nothing (caller's search
// degrades to BM25 only).
type EmbedHandler struct {
	svc       *frame.Service
	extractor *extraction.Extractor
}

// NewEmbedHandler ...
func NewEmbedHandler(svc *frame.Service, ex *extraction.Extractor) *EmbedHandler {
	return &EmbedHandler{svc: svc, extractor: ex}
}

// Name ...
func (h *EmbedHandler) Name() string { return eventsv1.TopicCanonicalsUpserted }

// PayloadType ...
func (h *EmbedHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

// Validate ...
func (h *EmbedHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("embed: empty payload")
	}
	return nil
}

// Execute embeds the canonical's text and emits EmbeddingV1.
func (h *EmbedHandler) Execute(ctx context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.CanonicalUpsertedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	c := env.Payload

	if h.extractor == nil {
		return nil // embedder disabled — search degrades to BM25
	}

	desc, _ := c.Attributes["description"].(string)
	text := strings.Join([]string{c.Title, c.IssuingEntity, desc}, " · ")
	vec, err := h.extractor.Embed(ctx, text)
	if err != nil {
		reason := classifyEmbedFailure(err)
		telemetry.RecordEmbedFailure(reason)
		util.Log(ctx).WithError(err).WithField("reason", reason).
			Warn("embed: provider failed, skipping")
		return nil // fail-open — no embedding is better than no row
	}
	if len(vec) == 0 {
		return nil // no embedding configured
	}

	out := eventsv1.EmbeddingV1{
		OpportunityID: c.OpportunityID,
		Vector:        vec,
		ModelVersion:  h.extractor.EmbedModelVersion(),
	}
	outEnv := eventsv1.NewEnvelope(eventsv1.TopicEmbeddings, out)
	return h.svc.EventsManager().Emit(ctx, eventsv1.TopicEmbeddings, outEnv)
}
