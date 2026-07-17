package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/pitabwire/util"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/jobqueue"
)

// Embedder produces a dense vector for opportunity text.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// EmbedStore is the subset of jobqueue.Store used by EmbedHandler.
type EmbedStore interface {
	SetEmbedding(ctx context.Context, canonicalID string, vec []float32) error
}

// FanOutPublisher publishes OpportunityFanOutV1 after a successful embed
// so matching can run Path A without waiting for digests.
type FanOutPublisher interface {
	PublishFanOut(ctx context.Context, job eventsv1.OpportunityFanOutV1) error
}

// EmbedHandler is a Frame Queue SubscribeWorker on SubjectWorkerEmbed.
// External embedding HTTP belongs on Frame Queue (decision tree: durable +
// slow I/O + retry), not on unmanaged goroutines or Frame Events.
type EmbedHandler struct {
	store    EmbedStore
	embedder Embedder
	fanout   FanOutPublisher // optional Path A handoff
}

// NewEmbedHandler builds a queue worker. embedder must be non-nil (caller
// only registers the subscriber when embeddings are configured).
func NewEmbedHandler(store EmbedStore, embedder Embedder) *EmbedHandler {
	return &EmbedHandler{store: store, embedder: embedder}
}

// WithFanOutPublisher enables post-embed Path A fan-out publish.
func (h *EmbedHandler) WithFanOutPublisher(p FanOutPublisher) *EmbedHandler {
	h.fanout = p
	return h
}

// Handle implements queue.SubscribeWorker. Returning a non-nil error
// redelivers the message (NATS ack_wait / max_deliver).
func (h *EmbedHandler) Handle(ctx context.Context, _ map[string]string, payload []byte) error {
	if h == nil || h.embedder == nil || h.store == nil {
		return nil
	}
	if len(payload) == 0 {
		return errors.New("embed: empty payload")
	}
	var env eventsv1.Envelope[eventsv1.OpportunityEmbedV1]
	if err := json.Unmarshal(payload, &env); err != nil {
		return fmt.Errorf("embed: decode envelope: %w", err)
	}
	job := env.Payload
	if strings.TrimSpace(job.OpportunityID) == "" {
		return errors.New("embed: opportunity_id required")
	}
	text := extraction.EmbedInput(job.Title, job.IssuingEntity, job.Description)
	vec, err := h.embedder.Embed(ctx, text)
	if err != nil {
		// Return error so Frame Queue retries with backoff.
		util.Log(ctx).WithError(err).WithField("canonical_id", job.OpportunityID).
			Warn("embed: provider failed; will retry")
		return fmt.Errorf("embed: provider: %w", err)
	}
	if len(vec) == 0 {
		// Not configured / empty response — ack (nothing to store).
		return nil
	}
	if err := h.store.SetEmbedding(ctx, job.OpportunityID, vec); err != nil {
		return fmt.Errorf("embed: set embedding: %w", err)
	}
	// Hand off to matching Path A (collect matches as jobs arrive).
	if h.fanout != nil {
		fo := eventsv1.OpportunityFanOutV1{
			OpportunityID: job.OpportunityID,
			Title:         job.Title,
			IssuingEntity: job.IssuingEntity,
			Description:   job.Description,
			Kind:          job.Kind,
			Country:       job.Country,
			AmountMax:     job.AmountMax,
			PostedAt:      job.PostedAt,
			Embedding:     vec,
		}
		if err := h.fanout.PublishFanOut(ctx, fo); err != nil {
			// Embed already persisted — log and continue; gap-fill/digest recover.
			util.Log(ctx).WithError(err).WithField("canonical_id", job.OpportunityID).
				Warn("embed: publish fan-out failed (non-fatal)")
		}
	}
	return nil
}

// Ensure EmbedHandler satisfies the queue worker surface without importing
// frame/queue into tests that only call Handle.
var _ interface {
	Handle(context.Context, map[string]string, []byte) error
} = (*EmbedHandler)(nil)

// Compile-time check that jobqueue.Store implements EmbedStore.
var _ EmbedStore = (*jobqueue.Store)(nil)
