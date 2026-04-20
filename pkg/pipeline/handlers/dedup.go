package handlers

import (
	"context"
	"errors"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/repository"
	"stawi.jobs/pkg/telemetry"
)

var dedupTracer = otel.Tracer("stawi.jobs.pipeline")

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
	if p.VariantID == "" {
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

	ctx, span := dedupTracer.Start(ctx, "pipeline.dedup")
	defer span.End()
	span.SetAttributes(
		attribute.String("variant_id", p.VariantID),
		attribute.String("source_id", p.SourceID),
	)

	start := time.Now()
	defer func() {
		if telemetry.StageDuration != nil {
			telemetry.StageDuration.Record(ctx, time.Since(start).Seconds(),
				metric.WithAttributes(attribute.String("stage", "dedup")),
			)
		}
	}()

	// 1. Load the variant.
	variant, err := h.jobRepo.GetVariantByID(ctx, p.VariantID)
	if err != nil {
		return err
	}
	if variant == nil {
		util.Log(ctx).WithField("variant_id", p.VariantID).Info("dedup: variant not found, skipping")
		return nil
	}

	// 2. Idempotency guard — only process raw variants.
	if variant.Stage != domain.StageRaw {
		return nil
	}

	// 3. Check for an existing variant with the same hard_key but a different ID.
	existing, err := h.jobRepo.FindByHardKey(ctx, variant.HardKey)
	if err != nil {
		return err
	}
	if existing != nil && existing.ID != variant.ID {
		// Duplicate — mark as flagged so stuck-variant recovery doesn't re-emit it.
		util.Log(ctx).
			WithField("variant_id", variant.ID).
			WithField("existing_id", existing.ID).
			WithField("hard_key", variant.HardKey).
			Info("dedup: variant is a duplicate, flagging")
		_ = h.jobRepo.UpdateStage(ctx, variant.ID, string(domain.StageFlagged))
		return nil
	}

	// 5. Advance stage to "deduped".
	if err := h.jobRepo.UpdateStage(ctx, variant.ID, string(domain.StageDeduped)); err != nil {
		return err
	}

	if telemetry.StageTransitions != nil {
		telemetry.StageTransitions.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("from", "raw"),
				attribute.String("to", "deduped"),
			),
		)
	}

	// 6. Emit the next pipeline event.
	return h.svc.EventsManager().Emit(ctx, EventVariantDeduped, &VariantPayload{
		VariantID: variant.ID,
		SourceID:  variant.SourceID,
	})
}
