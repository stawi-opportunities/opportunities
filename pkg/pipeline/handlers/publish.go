package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/pitabwire/frame"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/publish"
	"stawi.jobs/pkg/repository"
	"stawi.jobs/pkg/telemetry"
)

var publishTracer = otel.Tracer("stawi.jobs.publish")

// PublishHandler subscribes to job.ready events and writes JobSnapshot JSON to R2.
type PublishHandler struct {
	jobRepo    *repository.JobRepository
	publisher  *publish.R2Publisher
	purger     *publish.CachePurger
	svc        *frame.Service
	minQuality float64
}

// NewPublishHandler creates a PublishHandler. `purger` may be nil for local dev.
// `svc` may be nil, in which case the handler won't emit job.published
// downstream events (translator fan-out will just never fire).
func NewPublishHandler(
	jobRepo *repository.JobRepository,
	publisher *publish.R2Publisher,
	purger *publish.CachePurger,
	svc *frame.Service,
	minQuality float64,
) *PublishHandler {
	return &PublishHandler{
		jobRepo:    jobRepo,
		publisher:  publisher,
		purger:     purger,
		svc:        svc,
		minQuality: minQuality,
	}
}

func (h *PublishHandler) Name() string     { return EventJobReady }
func (h *PublishHandler) PayloadType() any { return &JobReadyPayload{} }

func (h *PublishHandler) Validate(_ context.Context, payload any) error {
	p, ok := payload.(*JobReadyPayload)
	if !ok {
		return errors.New("invalid payload type, expected *JobReadyPayload")
	}
	if p.CanonicalJobID == 0 {
		return errors.New("canonical_job_id is required")
	}
	return nil
}

func (h *PublishHandler) Execute(ctx context.Context, payload any) error {
	p, ok := payload.(*JobReadyPayload)
	if !ok {
		return errors.New("invalid payload type")
	}

	ctx, span := publishTracer.Start(ctx, "pipeline.publish")
	defer span.End()
	span.SetAttributes(attribute.Int64("canonical_job_id", p.CanonicalJobID))

	start := time.Now()
	defer func() {
		if telemetry.StageDuration != nil {
			telemetry.StageDuration.Record(ctx, time.Since(start).Seconds(),
				metric.WithAttributes(attribute.String("stage", "publish")),
			)
		}
	}()

	job, err := h.jobRepo.GetCanonicalByID(ctx, p.CanonicalJobID)
	if err != nil {
		return err
	}
	if job == nil {
		return nil
	}

	// Deletion path.
	if job.Status == "deleted" {
		return h.unpublish(ctx, job)
	}
	// Skip inactive / expired / low-quality.
	if job.Status != "active" {
		return nil
	}
	if job.QualityScore < h.minQuality {
		return nil
	}

	// Ensure slug exists (inherited from crawler spec).
	if job.Slug == "" {
		job.Slug = domain.BuildSlug(job.Title, job.Company, job.ID)
		_ = h.jobRepo.UpdateCanonicalFields(ctx, job.ID, map[string]any{"slug": job.Slug})
	}

	// Build and upload snapshot.
	descHTML := publish.RenderDescriptionHTML(job.Description)
	snap := domain.BuildSnapshotWithHTML(job, descHTML)
	body, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("snapshot marshal: %w", err)
	}
	key := "jobs/" + job.Slug + ".json"
	if err := h.publisher.UploadPublicSnapshot(ctx, key, body); err != nil {
		return fmt.Errorf("r2 upload %s: %w", key, err)
	}

	// Mark published + bump r2_version.
	nextVer := job.R2Version + 1
	if err := h.jobRepo.MarkPublished(ctx, job.ID, nextVer); err != nil {
		return fmt.Errorf("mark published: %w", err)
	}
	span.SetAttributes(attribute.Int("r2_version", nextVer))

	// Best-effort edge purge.
	if perr := h.purger.PurgeURL(ctx, publish.PublicURL(key)); perr != nil {
		span.RecordError(perr)
	}

	// Emit job.published so downstream handlers (translator fan-out) can
	// react without having to re-read the DB from a job.ready subscription.
	if h.svc != nil {
		if em := h.svc.EventsManager(); em != nil {
			if emitErr := em.Emit(ctx, EventJobPublished, &JobPublishedPayload{
				CanonicalJobID: job.ID,
				Slug:           job.Slug,
				SourceLang:     job.Language,
				R2Version:      nextVer,
			}); emitErr != nil {
				log.Printf("publish: emit %s for canonical %d (non-fatal): %v", EventJobPublished, job.ID, emitErr)
			}
		}
	}
	return nil
}

func (h *PublishHandler) unpublish(ctx context.Context, job *domain.CanonicalJob) error {
	if job.Slug == "" {
		return nil
	}
	key := "jobs/" + job.Slug + ".json"
	_ = h.publisher.Delete(ctx, key)
	_ = h.jobRepo.ClearPublished(ctx, job.ID)
	_ = h.purger.PurgeURL(ctx, publish.PublicURL(key))
	return nil
}
