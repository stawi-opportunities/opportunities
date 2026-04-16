package events

import (
	"context"
	"encoding/json"
	"errors"
	"log"

	"stawi.jobs/pkg/extraction"
	"stawi.jobs/pkg/repository"
)

// CandidateEmbeddingEventName is the event name for async candidate embedding generation.
const CandidateEmbeddingEventName = "candidate.embedding.needed"

// CandidateEmbeddingPayload carries the candidate ID and text to embed.
type CandidateEmbeddingPayload struct {
	CandidateID int64  `json:"candidate_id"`
	Text        string `json:"text"`
}

// CandidateEmbeddingHandler generates embeddings for candidate profiles.
type CandidateEmbeddingHandler struct {
	extractor     *extraction.Extractor
	candidateRepo *repository.CandidateRepository
}

// NewCandidateEmbeddingHandler creates a handler wired to the given extractor and repo.
func NewCandidateEmbeddingHandler(
	extractor *extraction.Extractor,
	candidateRepo *repository.CandidateRepository,
) *CandidateEmbeddingHandler {
	return &CandidateEmbeddingHandler{
		extractor:     extractor,
		candidateRepo: candidateRepo,
	}
}

// Name returns the event name this handler processes.
func (h *CandidateEmbeddingHandler) Name() string {
	return CandidateEmbeddingEventName
}

// PayloadType returns a zero-value pointer for JSON deserialization.
func (h *CandidateEmbeddingHandler) PayloadType() any {
	return &CandidateEmbeddingPayload{}
}

// Validate checks the payload before execution.
func (h *CandidateEmbeddingHandler) Validate(_ context.Context, payload any) error {
	p, ok := payload.(*CandidateEmbeddingPayload)
	if !ok {
		return errors.New("invalid payload type, expected *CandidateEmbeddingPayload")
	}
	if p.CandidateID == 0 {
		return errors.New("candidate_id is required")
	}
	if p.Text == "" {
		return errors.New("text is required")
	}
	return nil
}

// Execute generates an embedding for the candidate profile text and persists it.
func (h *CandidateEmbeddingHandler) Execute(ctx context.Context, payload any) error {
	p, ok := payload.(*CandidateEmbeddingPayload)
	if !ok {
		return errors.New("invalid payload type")
	}

	embedding, err := h.extractor.Embed(ctx, p.Text)
	if err != nil {
		log.Printf("candidate embedding event: failed for candidate %d: %v", p.CandidateID, err)
		return err
	}

	embJSON, err := json.Marshal(embedding)
	if err != nil {
		return err
	}

	return h.candidateRepo.UpdateEmbedding(ctx, p.CandidateID, string(embJSON))
}
