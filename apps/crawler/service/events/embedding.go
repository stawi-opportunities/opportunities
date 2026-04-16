package events

import (
	"context"
	"encoding/json"
	"errors"
	"log"

	"stawi.jobs/pkg/extraction"
	"stawi.jobs/pkg/repository"
)

// EmbeddingGenerationEventName is the event name for async embedding generation.
const EmbeddingGenerationEventName = "job.canonical.embedding"

// EmbeddingPayload carries the canonical job ID and text to embed.
type EmbeddingPayload struct {
	CanonicalJobID int64  `json:"canonical_job_id"`
	Title          string `json:"title"`
	Company        string `json:"company"`
	Description    string `json:"description"`
}

// EmbeddingGenerationHandler generates embeddings for canonical jobs.
// It implements a Name/PayloadType/Validate/Execute interface compatible with
// Frame event handlers, but can also be invoked directly via Execute.
type EmbeddingGenerationHandler struct {
	extractor *extraction.Extractor
	jobRepo   *repository.JobRepository
}

// NewEmbeddingGenerationHandler creates a handler wired to the given extractor and repo.
func NewEmbeddingGenerationHandler(
	extractor *extraction.Extractor,
	jobRepo *repository.JobRepository,
) *EmbeddingGenerationHandler {
	return &EmbeddingGenerationHandler{
		extractor: extractor,
		jobRepo:   jobRepo,
	}
}

// Name returns the event name this handler processes.
func (h *EmbeddingGenerationHandler) Name() string {
	return EmbeddingGenerationEventName
}

// PayloadType returns a zero-value pointer for JSON deserialization.
func (h *EmbeddingGenerationHandler) PayloadType() any {
	return &EmbeddingPayload{}
}

// Validate checks the payload before execution.
func (h *EmbeddingGenerationHandler) Validate(_ context.Context, payload any) error {
	p, ok := payload.(*EmbeddingPayload)
	if !ok {
		return errors.New("invalid payload type, expected *EmbeddingPayload")
	}
	if p.CanonicalJobID == 0 {
		return errors.New("canonical_job_id is required")
	}
	return nil
}

// Execute generates an embedding for the canonical job described by payload
// and persists it. Returns an error on failure (suitable for retry by a queue).
func (h *EmbeddingGenerationHandler) Execute(ctx context.Context, payload any) error {
	p, ok := payload.(*EmbeddingPayload)
	if !ok {
		return errors.New("invalid payload type")
	}

	text := p.Title + " " + p.Company + " " + p.Description
	embedding, err := h.extractor.Embed(ctx, text)
	if err != nil {
		log.Printf("embedding event: failed for canonical %d: %v", p.CanonicalJobID, err)
		return err
	}

	embJSON, err := json.Marshal(embedding)
	if err != nil {
		return err
	}

	return h.jobRepo.UpdateEmbedding(ctx, p.CanonicalJobID, string(embJSON))
}
