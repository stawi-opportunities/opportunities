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

// EmbedHandler is a Frame Queue subscriber on
// SubjectWorkerEmbed. It receives a CanonicalUpsertedV1 envelope,
// computes the embedding, and emits EmbeddingV1 onto the events bus.
// If no embedder is configured, it emits nothing (caller's search
// degrades to BM25 only).
//
// Embedding calls are external HTTP I/O (TEI / Cloudflare AI Gateway)
// that may take seconds and may fail. Per the Frame async decision
// tree these belong on a Queue with retry/backoff, not on the events
// bus. The dedup key is opportunity_id + model_version, so re-delivery
// from NATS is safe — re-embedding the same canonical produces the
// same vector modulo provider variance.
type EmbedHandler struct {
	svc       *frame.Service
	extractor *extraction.Extractor
}

// NewEmbedHandler ...
func NewEmbedHandler(svc *frame.Service, ex *extraction.Extractor) *EmbedHandler {
	return &EmbedHandler{svc: svc, extractor: ex}
}

// Handle implements queue.SubscribeWorker.
func (h *EmbedHandler) Handle(ctx context.Context, _ map[string]string, payload []byte) error {
	if len(payload) == 0 {
		return errors.New("embed: empty payload")
	}
	var env eventsv1.Envelope[eventsv1.CanonicalUpsertedV1]
	if err := json.Unmarshal(payload, &env); err != nil {
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
