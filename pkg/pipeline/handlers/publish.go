package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/publish"
	"stawi.jobs/pkg/repository"
	"stawi.jobs/pkg/services"
	"stawi.jobs/pkg/telemetry"
)

var publishTracer = otel.Tracer("stawi.jobs.publish")

// PublishHandler subscribes to job.ready events and writes JobSnapshot JSON to R2.
type PublishHandler struct {
	jobRepo         *repository.JobRepository
	publisher       *publish.R2Publisher
	purger          *publish.CachePurger
	svc             *frame.Service
	redirectClient  *services.RedirectClient
	redirectBaseURL string // e.g. "https://r.stawi.org" — what /r/{slug} resolves to publicly
	minQuality      float64
}

// NewPublishHandler creates a PublishHandler. `purger` may be nil for local dev.
// `svc` may be nil, in which case the handler won't emit job.published
// downstream events (translator fan-out will just never fire).
// `redirectClient` may also be nil — the handler will fall back to emitting
// the raw apply_url on the snapshot instead of a /r/{slug} tracked URL.
func NewPublishHandler(
	jobRepo *repository.JobRepository,
	publisher *publish.R2Publisher,
	purger *publish.CachePurger,
	svc *frame.Service,
	redirectClient *services.RedirectClient,
	redirectBaseURL string,
	minQuality float64,
) *PublishHandler {
	return &PublishHandler{
		jobRepo:         jobRepo,
		publisher:       publisher,
		purger:          purger,
		svc:             svc,
		redirectClient:  redirectClient,
		redirectBaseURL: strings.TrimRight(redirectBaseURL, "/"),
		minQuality:      minQuality,
	}
}

func (h *PublishHandler) Name() string     { return EventJobReady }
func (h *PublishHandler) PayloadType() any { return &JobReadyPayload{} }

func (h *PublishHandler) Validate(_ context.Context, payload any) error {
	p, ok := payload.(*JobReadyPayload)
	if !ok {
		return errors.New("invalid payload type, expected *JobReadyPayload")
	}
	if p.CanonicalJobID == "" {
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
	span.SetAttributes(attribute.String("canonical_job_id", p.CanonicalJobID))

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

	// Ensure slug exists. Dedupe pre-generates on Upsert using
	// ClusterID; this is the defensive path for legacy rows where
	// slug was never set. Use ClusterID as the hash input so
	// retroactively slugging matches what the dedupe engine would
	// produce today.
	if job.Slug == "" {
		job.Slug = domain.BuildSlug(job.Title, job.Company, job.ClusterID)
		_ = h.jobRepo.UpdateCanonicalFields(ctx, job.ID, map[string]any{"slug": job.Slug})
	}

	// Create (or reuse) the redirect-service link for this job's
	// apply_url. First publish creates; subsequent re-publishes reuse
	// the stored RedirectSlug so external bookmarks to /r/{slug} stay
	// valid across re-crawls.
	applyURL := job.ApplyURL
	if h.redirectClient != nil && job.ApplyURL != "" && job.RedirectSlug == "" {
		link, lerr := h.redirectClient.CreateLink(ctx, &services.RedirectLink{
			DestinationURL: job.ApplyURL,
			AffiliateID:    "canonical_job_" + job.ID,
			Campaign:       "jobs",
			Source:         "stawi-jobs",
			Medium:         "organic",
		})
		if lerr != nil {
			// Non-fatal — we'll just ship the raw apply_url on this
			// publish and try again on the next re-publish.
			util.Log(ctx).WithError(lerr).WithField("canonical_job_id", job.ID).
				Warn("publish: create redirect link failed")
		} else if link != nil && link.Slug != "" {
			job.RedirectLinkID = link.ID
			job.RedirectSlug = link.Slug
			if err := h.jobRepo.SetRedirectLink(ctx, job.ID, link.ID, link.Slug); err != nil {
				util.Log(ctx).WithError(err).WithField("canonical_job_id", job.ID).
					Warn("publish: persist redirect link failed")
			}
		}
	}
	// Swap apply_url to the tracked /r/{slug} so every click flows
	// through the redirect service. redirectBaseURL is the public
	// origin the redirect service is exposed at; absent that we fall
	// back to the raw URL.
	if job.RedirectSlug != "" && h.redirectBaseURL != "" {
		applyURL = h.redirectBaseURL + "/r/" + job.RedirectSlug
	}

	// Build and upload snapshot. Keep the original apply_url on the
	// DB row but emit the tracked URL on the public JSON.
	descHTML := publish.RenderDescriptionHTML(job.Description)
	snap := domain.BuildSnapshotWithHTML(job, descHTML)
	snap.ApplyURL = applyURL
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
				util.Log(ctx).WithError(emitErr).
					WithField("event", EventJobPublished).
					WithField("canonical_job_id", job.ID).
					Warn("publish: emit downstream event failed")
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

	// Expire the redirect link so /r/{slug} stops forwarding to a
	// posting we've taken down. The link record itself stays in the
	// redirect service so historical click data remains queryable.
	if h.redirectClient != nil && job.RedirectLinkID != "" {
		if err := h.redirectClient.ExpireLink(ctx, job.RedirectLinkID); err != nil {
			util.Log(ctx).WithError(err).
				WithField("redirect_link_id", job.RedirectLinkID).
				Warn("publish: expire redirect link failed")
		}
	}
	return nil
}
