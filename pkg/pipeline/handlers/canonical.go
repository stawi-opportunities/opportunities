package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"stawi.jobs/pkg/archive"
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
	archive      archive.Archive
	rawRefRepo   *repository.RawRefRepository
	svc          *frame.Service
}

// NewCanonicalHandler creates a CanonicalHandler wired to the given dependencies.
func NewCanonicalHandler(
	jobRepo *repository.JobRepository,
	dedupeEngine *dedupe.Engine,
	extractor *extraction.Extractor,
	arch archive.Archive,
	rawRefRepo *repository.RawRefRepository,
	svc *frame.Service,
) *CanonicalHandler {
	return &CanonicalHandler{
		jobRepo:      jobRepo,
		dedupeEngine: dedupeEngine,
		extractor:    extractor,
		archive:      arch,
		rawRefRepo:   rawRefRepo,
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
	if p.VariantID == "" {
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
		attribute.String("variant_id", p.VariantID),
		attribute.String("source_id", p.SourceID),
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
		util.Log(ctx).WithField("variant_id", p.VariantID).Info("canonical: variant not found, skipping")
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

	// 3b. Slug is pre-generated inside dedupeEngine.UpsertAndCluster
	//     using ClusterID (see pkg/dedupe/dedupe.go). This fallback
	//     remains for the rare case where a legacy row exists without
	//     a slug — defensive only; new rows always arrive slugged.
	if canonical != nil && canonical.Slug == "" {
		canonical.Slug = domain.BuildSlug(canonical.Title, canonical.Company, canonical.ClusterID)
		if slugErr := h.jobRepo.UpdateCanonicalFields(ctx, canonical.ID, map[string]any{"slug": canonical.Slug}); slugErr != nil {
			util.Log(ctx).WithError(slugErr).WithField("canonical_job_id", canonical.ID).Warn("canonical: set slug failed (non-fatal)")
		}
	}

	// 4. Generate and store embedding (non-fatal on failure).
	if canonical != nil {
		embText := canonical.Title + " " + canonical.Skills + " " + canonical.Description
		embedding, embErr := h.extractor.Embed(ctx, embText)
		if embErr != nil {
			util.Log(ctx).WithError(embErr).WithField("canonical_job_id", canonical.ID).Warn("canonical: embedding failed (non-fatal)")
		} else {
			embJSON, marshalErr := json.Marshal(embedding)
			if marshalErr != nil {
				util.Log(ctx).WithError(marshalErr).WithField("canonical_job_id", canonical.ID).Warn("canonical: marshal embedding failed (non-fatal)")
			} else {
				if storeErr := h.jobRepo.UpdateEmbedding(ctx, canonical.ID, string(embJSON)); storeErr != nil {
					util.Log(ctx).WithError(storeErr).WithField("canonical_job_id", canonical.ID).Warn("canonical: store embedding failed (non-fatal)")
				}
			}
		}

		// Recompute quality score with latest data.
		if scoreErr := h.jobRepo.RecomputeQualityScore(ctx, canonical.ID); scoreErr != nil {
			util.Log(ctx).WithError(scoreErr).WithField("canonical_job_id", canonical.ID).Warn("canonical: recompute quality score failed (non-fatal)")
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

	// Record cluster membership on the variant so the normalize
	// handler's archive write can find the right cluster on later
	// re-runs (e.g. reprocessing).
	if variant.ClusterID == "" && canonical != nil {
		if err := h.jobRepo.UpdateVariantFields(ctx, variant.ID, map[string]any{
			"cluster_id": canonical.ClusterID,
		}); err != nil {
			util.Log(ctx).WithError(err).
				WithField("variant_id", variant.ID).
				WithField("canonical_job_id", canonical.ID).
				Warn("canonical: backfill cluster_id failed (non-fatal)")
		}
		variant.ClusterID = canonical.ClusterID
	}

	// Persist canonical snapshot + manifest to archive.
	if canonical != nil && h.archive != nil {
		snap := archive.CanonicalSnapshot{
			ID:             canonical.ID,
			ClusterID:      canonical.ClusterID,
			Slug:           canonical.Slug,
			Title:          canonical.Title,
			Company:        canonical.Company,
			Description:    canonical.Description,
			LocationText:   canonical.LocationText,
			Country:        canonical.Country,
			Language:       canonical.Language,
			RemoteType:     canonical.RemoteType,
			EmploymentType: canonical.EmploymentType,
			SalaryMin:      canonical.SalaryMin,
			SalaryMax:      canonical.SalaryMax,
			Currency:       canonical.Currency,
			ApplyURL:       canonical.ApplyURL,
			QualityScore:   canonical.QualityScore,
			PostedAt:       canonical.PostedAt,
			FirstSeenAt:    canonical.FirstSeenAt,
			LastSeenAt:     canonical.LastSeenAt,
			Status:         canonical.Status,
			Category:       canonical.Category,
			R2Version:      canonical.R2Version,
			WrittenAt:      time.Now().UTC(),
		}
		if err := h.archive.PutCanonical(ctx, canonical.ClusterID, snap); err != nil {
			util.Log(ctx).WithError(err).WithField("canonical_job_id", canonical.ID).
				Warn("canonical: archive PutCanonical failed (non-fatal)")
		}

		// Rebuild the manifest from current DB state.
		if err := h.rebuildManifest(ctx, canonical); err != nil {
			util.Log(ctx).WithError(err).WithField("canonical_job_id", canonical.ID).
				Warn("canonical: rebuild manifest failed (non-fatal)")
		}

		// Register the raw→cluster ref so the purge sweeper can GC
		// this hash when the cluster is eventually torn down.
		if variant.RawContentHash != "" && h.rawRefRepo != nil {
			if err := h.rawRefRepo.Upsert(ctx, variant.RawContentHash, canonical.ClusterID, variant.ID); err != nil {
				util.Log(ctx).WithError(err).
					WithField("canonical_job_id", canonical.ID).
					WithField("content_hash", variant.RawContentHash).
					Warn("canonical: raw_ref upsert failed (non-fatal)")
			}
		}
	}

	// 6. Emit job.ready.
	return h.svc.EventsManager().Emit(ctx, EventJobReady, &JobReadyPayload{
		CanonicalJobID: func() string {
			if canonical != nil {
				return canonical.ID
			}
			return ""
		}(),
	})
}

// rebuildManifest rewrites clusters/{cluster_id}/manifest.json from
// current DB state. Called after every canonical upsert; cheap because
// each cluster typically has 1–5 variants.
func (h *CanonicalHandler) rebuildManifest(ctx context.Context, canonical *domain.CanonicalJob) error {
	type variantRow struct {
		ID             string
		SourceID       string
		RawContentHash string
		ScrapedAt      time.Time
	}
	var variants []variantRow
	if err := h.jobRepo.DB(ctx, true).
		Table("job_variants").
		Select("id, source_id, raw_content_hash, scraped_at").
		Where("cluster_id = ?", canonical.ClusterID).
		Scan(&variants).Error; err != nil {
		return err
	}

	m := archive.Manifest{
		ClusterID:   canonical.ClusterID,
		CanonicalID: canonical.ID,
		Slug:        canonical.Slug,
		UpdatedAt:   time.Now().UTC(),
	}
	for _, v := range variants {
		m.Variants = append(m.Variants, archive.ManifestVariant{
			VariantID:      v.ID,
			SourceID:       v.SourceID,
			RawContentHash: v.RawContentHash,
			ScrapedAt:      v.ScrapedAt,
		})
	}
	return h.archive.PutManifest(ctx, canonical.ClusterID, m)
}
