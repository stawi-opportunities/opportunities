package handlers

import (
	"context"
	"errors"
	"hash/fnv"
	"log"

	"github.com/pitabwire/frame"

	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/repository"
)

// DedupHandler processes variant.raw.stored events and advances deduplicated
// variants to the variant.deduped stage.
type DedupHandler struct {
	jobRepo *repository.JobRepository
	svc     *frame.Service
}

// NewDedupHandler creates a DedupHandler wired to the given repository and service.
func NewDedupHandler(jobRepo *repository.JobRepository, svc *frame.Service) *DedupHandler {
	return &DedupHandler{
		jobRepo: jobRepo,
		svc:     svc,
	}
}

// Name returns the event name this handler processes.
func (h *DedupHandler) Name() string {
	return EventVariantRawStored
}

// PayloadType returns a zero-value pointer for JSON deserialization.
func (h *DedupHandler) PayloadType() any {
	return &VariantPayload{}
}

// Validate checks the payload before execution.
func (h *DedupHandler) Validate(_ context.Context, payload any) error {
	p, ok := payload.(*VariantPayload)
	if !ok {
		return errors.New("invalid payload type, expected *VariantPayload")
	}
	if p.VariantID == 0 {
		return errors.New("variant_id is required")
	}
	return nil
}

// Execute deduplicates the variant described by payload and, if unique,
// advances it to the "deduped" stage and emits variant.deduped.
func (h *DedupHandler) Execute(ctx context.Context, payload any) error {
	p, ok := payload.(*VariantPayload)
	if !ok {
		return errors.New("invalid payload type")
	}

	// 1. Load the variant.
	variant, err := h.jobRepo.GetVariantByID(ctx, p.VariantID)
	if err != nil {
		return err
	}
	if variant == nil {
		log.Printf("dedup: variant %d not found, skipping", p.VariantID)
		return nil
	}

	// 2. Idempotency guard — only process raw variants.
	if variant.Stage != domain.StageRaw {
		return nil
	}

	// 3. Acquire advisory lock keyed on the hard_key hash to serialise
	//    concurrent dedup checks for the same logical posting.
	h64 := fnv.New64a()
	_, _ = h64.Write([]byte(variant.HardKey))
	lockKey := int64(h64.Sum64()) // nolint:gosec — intentional truncation

	if err := h.jobRepo.AdvisoryLock(ctx, lockKey); err != nil {
		return err
	}
	defer func() {
		_ = h.jobRepo.AdvisoryUnlock(ctx, lockKey)
	}()

	// 4. Check for an existing variant with the same hard_key but a different ID.
	existing, err := h.jobRepo.FindByHardKey(ctx, variant.HardKey)
	if err != nil {
		return err
	}
	if existing != nil && existing.ID != variant.ID {
		// Duplicate — skip further processing.
		log.Printf("dedup: variant %d is a duplicate of %d (hard_key=%s), skipping",
			variant.ID, existing.ID, variant.HardKey)
		return nil
	}

	// 5. Advance stage to "deduped".
	if err := h.jobRepo.UpdateStage(ctx, variant.ID, string(domain.StageDeduped)); err != nil {
		return err
	}

	// 6. Emit the next pipeline event.
	return h.svc.EventsManager().Emit(ctx, EventVariantDeduped, &VariantPayload{
		VariantID: variant.ID,
		SourceID:  variant.SourceID,
	})
}
