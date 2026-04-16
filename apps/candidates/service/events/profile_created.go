package events

import (
	"context"
	"errors"
	"log"

	"stawi.jobs/pkg/repository"
)

// ProfileCreatedEventName is the event name fired when a candidate profile is created.
const ProfileCreatedEventName = "candidate.profile.created"

// ProfileCreatedPayload carries the candidate ID for the newly created profile.
type ProfileCreatedPayload struct {
	CandidateID int64 `json:"candidate_id"`
}

// ProfileCreatedHandler processes candidate.profile.created events.
type ProfileCreatedHandler struct {
	candidateRepo *repository.CandidateRepository
}

// NewProfileCreatedHandler creates a handler wired to the given repository.
func NewProfileCreatedHandler(candidateRepo *repository.CandidateRepository) *ProfileCreatedHandler {
	return &ProfileCreatedHandler{candidateRepo: candidateRepo}
}

// Name returns the event name this handler processes.
func (h *ProfileCreatedHandler) Name() string {
	return ProfileCreatedEventName
}

// PayloadType returns a zero-value pointer for JSON deserialization.
func (h *ProfileCreatedHandler) PayloadType() any {
	return &ProfileCreatedPayload{}
}

// Validate checks the payload before execution.
func (h *ProfileCreatedHandler) Validate(_ context.Context, payload any) error {
	p, ok := payload.(*ProfileCreatedPayload)
	if !ok {
		return errors.New("invalid payload type, expected *ProfileCreatedPayload")
	}
	if p.CandidateID == 0 {
		return errors.New("candidate_id is required")
	}
	return nil
}

// Execute handles the profile created event. Currently logs receipt; matching
// will be wired when the matcher package is available.
func (h *ProfileCreatedHandler) Execute(ctx context.Context, payload any) error {
	p, ok := payload.(*ProfileCreatedPayload)
	if !ok {
		return errors.New("invalid payload type")
	}

	candidate, err := h.candidateRepo.GetByID(ctx, p.CandidateID)
	if err != nil {
		log.Printf("profile_created event: failed to load candidate %d: %v", p.CandidateID, err)
		return err
	}
	if candidate == nil {
		log.Printf("profile_created event: candidate %d not found", p.CandidateID)
		return nil
	}

	log.Printf("profile_created event: received for candidate %d (status=%s, has_embedding=%v)",
		p.CandidateID, candidate.Status, candidate.Embedding != "")

	// TODO: when pkg/matching is available, run matching here for active
	// candidates that have an embedding.

	return nil
}
