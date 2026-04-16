package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/pitabwire/frame"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"stawi.jobs/pkg/dedupe"
	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/extraction"
	"stawi.jobs/pkg/repository"
	"stawi.jobs/pkg/telemetry"
)

var canonicalTracer = otel.Tracer("stawi.jobs.pipeline")

// CanonicalHandler processes variant.validated events, runs deduplication and
// clustering, generates embeddings, and advances variants to the ready stage.
type CanonicalHandler struct {
	jobRepo      *repository.JobRepository
	dedupeEngine *dedupe.Engine
	extractor    *extraction.Extractor
	svc          *frame.Service
}

// NewCanonicalHandler creates a CanonicalHandler wired to the given dependencies.
func NewCanonicalHandler(
	jobRepo *repository.JobRepository,
	dedupeEngine *dedupe.Engine,
	extractor *extraction.Extractor,
	svc *frame.Service,
) *CanonicalHandler {
	return &CanonicalHandler{
		jobRepo:      jobRepo,
		dedupeEngine: dedupeEngine,
		extractor:    extractor,
		svc:          svc,
	}
}

// Name returns the event name this handler processes.
func (h *CanonicalHandler) Name() string {
	return EventVariantValidated
}

// PayloadType returns a zero-value pointer for JSON deserialization.
func (h *CanonicalHandler) PayloadType() any {
	return &VariantPayload{}
}

// Validate checks the payload before execution.
func (h *CanonicalHandler) Validate(_ context.Context, payload any) error {
	p, ok := payload.(*VariantPayload)
	if !ok {
		return errors.New("invalid payload type, expected *VariantPayload")
	}
	if p.VariantID == 0 {
		return errors.New("variant_id is required")
	}
	return nil
}

// Execute deduplicates the validated variant, generates an embedding for the
// canonical job, recomputes its quality score, and emits job.ready.
func (h *CanonicalHandler) Execute(ctx context.Context, payload any) error {
	p, ok := payload.(*VariantPayload)
	if !ok {
		return errors.New("invalid payload type")
	}

	ctx, span := canonicalTracer.Start(ctx, "pipeline.canonical")
	defer span.End()
	span.SetAttributes(
		attribute.Int64("variant_id", p.VariantID),
		attribute.Int64("source_id", p.SourceID),
	)

	start := time.Now()
	defer func() {
		if telemetry.StageDuration != nil {
			telemetry.StageDuration.Record(ctx, time.Since(start).Seconds(),
				metric.WithAttributes(attribute.String("stage", "canonical")),
			)
		}
	}()

	// 1. Load the variant.
	variant, err := h.jobRepo.GetVariantByID(ctx, p.VariantID)
	if err != nil {
		return err
	}
	if variant == nil {
		log.Printf("canonical: variant %d not found, skipping", p.VariantID)
		return nil
	}

	// 2. Idempotency guard — only process validated variants.
	if variant.Stage != domain.StageValidated {
		return nil
	}

	// 3. Upsert variant into dedupe/cluster pipeline and obtain canonical.
	canonical, err := h.dedupeEngine.UpsertAndCluster(ctx, variant)
	if err != nil {
		return err
	}

	// 3b. Generate permanent slug if not yet set.
	if canonical != nil && canonical.Slug == "" {
		canonical.Slug = domain.BuildSlug(canonical.Title, canonical.Company, canonical.ID)
		if slugErr := h.jobRepo.UpdateCanonicalFields(ctx, canonical.ID, map[string]any{"slug": canonical.Slug}); slugErr != nil {
			log.Printf("canonical: set slug for canonical %d (non-fatal): %v", canonical.ID, slugErr)
		}
	}

	// 4. Generate and store embedding (non-fatal on failure).
	if canonical != nil {
		embText := canonical.Title + " " + canonical.Skills + " " + canonical.Description
		embedding, embErr := h.extractor.Embed(ctx, embText)
		if embErr != nil {
			log.Printf("canonical: embedding failed for canonical %d (non-fatal): %v", canonical.ID, embErr)
		} else {
			embJSON, marshalErr := json.Marshal(embedding)
			if marshalErr != nil {
				log.Printf("canonical: marshal embedding for canonical %d (non-fatal): %v", canonical.ID, marshalErr)
			} else {
				if storeErr := h.jobRepo.UpdateEmbedding(ctx, canonical.ID, string(embJSON)); storeErr != nil {
					log.Printf("canonical: store embedding for canonical %d (non-fatal): %v", canonical.ID, storeErr)
				}
			}
		}

		// Recompute quality score with latest data.
		if scoreErr := h.jobRepo.RecomputeQualityScore(ctx, canonical.ID); scoreErr != nil {
			log.Printf("canonical: recompute quality score for canonical %d (non-fatal): %v", canonical.ID, scoreErr)
		}
	}

	// 5. Advance stage to "ready".
	if err := h.jobRepo.UpdateStage(ctx, variant.ID, string(domain.StageReady)); err != nil {
		return err
	}

	if telemetry.StageTransitions != nil {
		telemetry.StageTransitions.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("from", "validated"),
				attribute.String("to", "ready"),
			),
		)
	}
	if telemetry.JobsReady != nil {
		telemetry.JobsReady.Add(ctx, 1)
	}

	// 6. Emit job.ready.
	return h.svc.EventsManager().Emit(ctx, EventJobReady, &JobReadyPayload{
		CanonicalJobID: func() int64 {
			if canonical != nil {
				return canonical.ID
			}
			return 0
		}(),
	})
}
