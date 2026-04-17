package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/publish"
	"stawi.jobs/pkg/repository"
	"stawi.jobs/pkg/telemetry"
)

var publishTracer = otel.Tracer("stawi.jobs.publish")

// PublishHandler subscribes to job.ready events and uploads job markdown to R2.
type PublishHandler struct {
	jobRepo      *repository.JobRepository
	publisher    *publish.R2Publisher
	minQuality   float64
	publishCount atomic.Int64
	batchSize    int64
}

// NewPublishHandler creates a PublishHandler.
func NewPublishHandler(
	jobRepo *repository.JobRepository,
	publisher *publish.R2Publisher,
	minQuality float64,
) *PublishHandler {
	return &PublishHandler{
		jobRepo:    jobRepo,
		publisher:  publisher,
		minQuality: minQuality,
		batchSize:  50,
	}
}

func (h *PublishHandler) Name() string {
	return EventJobReady
}

func (h *PublishHandler) PayloadType() any {
	return &JobReadyPayload{}
}

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

	// 1. Load the canonical job.
	job, err := h.jobRepo.GetCanonicalByID(ctx, p.CanonicalJobID)
	if err != nil {
		return err
	}
	if job == nil {
		log.Printf("publish: canonical job %d not found, skipping", p.CanonicalJobID)
		return nil
	}

	// 2. Skip if below quality threshold or inactive.
	if job.QualityScore < h.minQuality {
		log.Printf("publish: canonical job %d quality %.1f below threshold %.1f, skipping",
			job.ID, job.QualityScore, h.minQuality)
		return nil
	}
	if job.Status != "active" {
		log.Printf("publish: canonical job %d status=%s, skipping", job.ID, job.Status)
		return nil
	}

	// 3. Ensure slug exists.
	if job.Slug == "" {
		job.Slug = domain.BuildSlug(job.Title, job.Company, job.ID)
		if slugErr := h.jobRepo.UpdateCanonicalFields(ctx, job.ID, map[string]any{"slug": job.Slug}); slugErr != nil {
			log.Printf("publish: set slug for job %d (non-fatal): %v", job.ID, slugErr)
		}
	}

	// 4. Render markdown.
	md := publish.RenderJobMarkdown(job)

	// 5. Upload to R2.
	key := "jobs/" + job.Slug + ".md"
	if err := h.publisher.Upload(ctx, key, md); err != nil {
		return fmt.Errorf("publish: upload %s to R2: %w", key, err)
	}

	log.Printf("publish: uploaded %s (%d bytes)", key, len(md))

	// 6. Trigger deploy hook every batchSize publishes.
	count := h.publishCount.Add(1)
	if count%h.batchSize == 0 {
		if hookErr := h.publisher.TriggerDeploy(); hookErr != nil {
			log.Printf("publish: deploy hook failed (non-fatal): %v", hookErr)
		}
	}

	return nil
}
