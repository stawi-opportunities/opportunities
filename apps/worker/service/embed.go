package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/telemetry"
	"github.com/stawi-opportunities/opportunities/pkg/variantstate"
)

// maxEmbedAttempts bounds redelivery for the embed queue worker. The
// worker subject runs on a stream with no app-level max-deliver/DLQ, so a
// persistent TEI failure would otherwise redeliver forever. After this
// many recorded attempts we ack and leave the embedding NULL — the
// embedding backfill job re-derives it later. Each redelivery increments
// the per-canonical `embed` attempts counter in the variant ledger.
const maxEmbedAttempts = 5

// isRetryableEmbed reports whether an embed-provider error is worth a
// redelivery (HTTP 429 / 5xx, connection/network errors, "no response
// from stream") versus a clearly non-retryable client error (HTTP 4xx
// other than 429) that would just fail again on every redelivery.
func isRetryableEmbed(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	// Explicit non-retryable client errors: a malformed request won't
	// succeed on redelivery, so ack + drop instead of looping.
	switch {
	case strings.Contains(msg, "400"), strings.Contains(msg, "bad request"),
		strings.Contains(msg, "401"), strings.Contains(msg, "403"),
		strings.Contains(msg, "404"), strings.Contains(msg, "422"):
		return false
	}
	switch {
	case strings.Contains(msg, "429"), strings.Contains(msg, "rate limit"):
		return true
	case strings.Contains(msg, "500"), strings.Contains(msg, "502"),
		strings.Contains(msg, "503"), strings.Contains(msg, "504"):
		return true
	case strings.Contains(msg, "no response from stream"):
		return true
	case strings.Contains(msg, "connection"), strings.Contains(msg, "network"),
		strings.Contains(msg, "no such host"), strings.Contains(msg, "timeout"),
		strings.Contains(msg, "context deadline"), strings.Contains(msg, "eof"),
		strings.Contains(msg, "reset by peer"):
		return true
	}
	// Unknown errors: prefer retry over silent drop — embeddings are
	// expensive to lose and the bounded attempt cap stops an infinite loop.
	return true
}

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
	store     *variantstate.Store // nil-safe; soft-fails on Postgres outage
}

// NewEmbedHandler ...
func NewEmbedHandler(svc *frame.Service, ex *extraction.Extractor, store *variantstate.Store) *EmbedHandler {
	return &EmbedHandler{svc: svc, extractor: ex, store: store}
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
	text := extraction.EmbedInput(c.Title, c.IssuingEntity, desc)
	vec, err := h.extractor.Embed(ctx, text)
	if err != nil {
		reason := classifyEmbedFailure(err)
		telemetry.RecordEmbedFailure(reason)
		wrapped := fmt.Errorf("embed: provider failed (%s): %w", reason, err)
		// Record the failure against the canonical's variants so a stuck
		// embed is visible in the ledger and the attempt counter advances.
		_ = h.store.RecordErrorByCanonical(ctx, c.OpportunityID, variantstate.StageEmbed, wrapped)

		// Non-retryable client error (e.g. 400 bad request): redelivery
		// would just fail again, so ack + drop. The embedding stays NULL
		// for the backfill job to pick up.
		if !isRetryableEmbed(err) {
			util.Log(ctx).WithError(err).WithField("reason", reason).
				Warn("embed: non-retryable provider error; acking and leaving embedding NULL")
			return nil
		}

		// Retryable (429 / 5xx / connection / "no response from stream").
		// The worker subject has no app-level max-deliver/DLQ, so bound
		// redelivery here: after maxEmbedAttempts recorded attempts, ack
		// and leave the embedding NULL for the backfill rather than loop
		// forever and occupy a queue slot.
		attempts, _ := h.store.AttemptCountByCanonical(ctx, c.OpportunityID, variantstate.StageEmbed)
		if attempts >= maxEmbedAttempts {
			util.Log(ctx).WithError(err).
				WithField("reason", reason).
				WithField("attempts", attempts).
				Warn("embed: retryable error exceeded max attempts; acking and leaving embedding NULL")
			return nil
		}

		// Return the error so the queue redelivers with backoff. Do NOT
		// drop the embedding on a transient failure.
		util.Log(ctx).WithError(err).
			WithField("reason", reason).
			WithField("attempts", attempts).
			Warn("embed: retryable provider error; nacking for redelivery")
		return wrapped
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
