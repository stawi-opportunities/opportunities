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
	"stawi.jobs/pkg/telemetry"
	"stawi.jobs/pkg/translate"
)

var translateTracer = otel.Tracer("stawi.jobs.translate")

// TranslateHandler fans a published job snapshot out into every configured
// target language and uploads each translation to R2 at
// jobs/{slug}.{lang}.json. Translations are not persisted in Postgres —
// CanonicalJob.TranslatedAt / TranslatedLangs only record *which* languages
// made it out.
type TranslateHandler struct {
	jobRepo    *repository.JobRepository
	publisher  *publish.R2Publisher
	purger     *publish.CachePurger
	translator *translate.Translator
	svc        *frame.Service
	langs      []string
	minQuality float64
}

// NewTranslateHandler returns a new TranslateHandler. If translator or
// publisher is nil, or langs is empty, the handler becomes a no-op — this
// is the switch ops use to disable fan-out without unregistering the handler.
func NewTranslateHandler(
	jobRepo *repository.JobRepository,
	publisher *publish.R2Publisher,
	purger *publish.CachePurger,
	translator *translate.Translator,
	svc *frame.Service,
	langs []string,
	minQuality float64,
) *TranslateHandler {
	return &TranslateHandler{
		jobRepo:    jobRepo,
		publisher:  publisher,
		purger:     purger,
		translator: translator,
		svc:        svc,
		langs:      translate.Normalize(langs),
		minQuality: minQuality,
	}
}

func (h *TranslateHandler) Name() string     { return EventJobPublished }
func (h *TranslateHandler) PayloadType() any { return &JobPublishedPayload{} }

func (h *TranslateHandler) Validate(_ context.Context, payload any) error {
	p, ok := payload.(*JobPublishedPayload)
	if !ok {
		return errors.New("invalid payload type, expected *JobPublishedPayload")
	}
	if p.CanonicalJobID == "" {
		return errors.New("canonical_job_id is required")
	}
	return nil
}

func (h *TranslateHandler) Execute(ctx context.Context, payload any) error {
	p, ok := payload.(*JobPublishedPayload)
	if !ok {
		return errors.New("invalid payload type")
	}

	// Feature gates. Each of these turns fan-out into a no-op cleanly
	// rather than propagating an error up through the event bus.
	if h.translator == nil || h.publisher == nil || len(h.langs) == 0 {
		return nil
	}

	ctx, span := translateTracer.Start(ctx, "pipeline.translate")
	defer span.End()
	span.SetAttributes(
		attribute.String("canonical_job_id", p.CanonicalJobID),
		attribute.String("source_lang", p.SourceLang),
	)

	start := time.Now()
	defer func() {
		if telemetry.StageDuration != nil {
			telemetry.StageDuration.Record(ctx, time.Since(start).Seconds(),
				metric.WithAttributes(attribute.String("stage", "translate")),
			)
		}
	}()

	job, err := h.jobRepo.GetCanonicalByID(ctx, p.CanonicalJobID)
	if err != nil {
		return err
	}
	if job == nil || job.Status != "active" {
		return nil
	}
	if job.QualityScore < h.minQuality {
		return nil
	}

	sourceLang := strings.ToLower(strings.TrimSpace(p.SourceLang))
	if sourceLang == "" {
		sourceLang = strings.ToLower(strings.TrimSpace(job.Language))
	}
	if sourceLang == "" {
		sourceLang = "en"
	}

	// Build the base snapshot once and clone for each target language so we
	// don't re-marshal redundant fields per call.
	descHTML := publish.RenderDescriptionHTML(job.Description)
	base := domain.BuildSnapshotWithHTML(job, descHTML)

	completed := make([]string, 0, len(h.langs))
	for _, tgt := range h.langs {
		if tgt == sourceLang {
			// Skip: the source-language snapshot is already at the
			// primary key (jobs/{slug}.json).
			continue
		}

		translated, terr := h.translator.Translate(ctx, base, sourceLang, tgt)
		if terr != nil {
			// Best-effort fan-out — log and move on. A failed language
			// isn't fatal; the UI will fall back to the source snapshot.
			util.Log(ctx).WithError(terr).
				WithField("source_lang", sourceLang).
				WithField("target_lang", tgt).
				WithField("canonical_job_id", job.ID).
				Warn("translate: LLM call failed")
			continue
		}

		body, merr := json.Marshal(translated)
		if merr != nil {
			util.Log(ctx).WithError(merr).
				WithField("target_lang", tgt).
				WithField("canonical_job_id", job.ID).
				Warn("translate: marshal failed")
			continue
		}

		key := fmt.Sprintf("jobs/%s.%s.json", job.Slug, tgt)
		if uerr := h.publisher.UploadPublicSnapshot(ctx, key, body); uerr != nil {
			util.Log(ctx).WithError(uerr).
				WithField("r2_key", key).
				Warn("translate: R2 upload failed")
			continue
		}

		completed = append(completed, tgt)

		// Best-effort edge purge; don't fail the handler if purge errs.
		if h.purger != nil {
			_ = h.purger.PurgeURL(ctx, publish.PublicURL(key))
		}
	}

	// Record which languages successfully shipped. Zero completed
	// languages still stamps translated_at — we tried — so the handler
	// doesn't spin forever on a persistently broken LLM.
	langsCSV := strings.Join(completed, ",")
	if err := h.jobRepo.MarkTranslated(ctx, job.ID, langsCSV); err != nil {
		return fmt.Errorf("mark translated: %w", err)
	}
	span.SetAttributes(attribute.String("translated_langs", langsCSV))
	return nil
}
