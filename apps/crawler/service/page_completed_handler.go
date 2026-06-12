package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/telemetry"
)

// SourceHealthRepo is the slice of SourceRepository used to reconcile
// a crawl's outcome back onto the Postgres sources row. Narrow so
// tests can fake it without ceremony.
type SourceHealthRepo interface {
	GetByID(ctx context.Context, id string) (*domain.Source, error)
	RecordSuccess(ctx context.Context, id string, newHealth float64) error
	RecordFailure(ctx context.Context, id string, newHealth float64, consecutive int) error
	FlagNeedsTuning(ctx context.Context, id string, flag bool) error
	UpdateNextCrawl(ctx context.Context, id string, next, verified time.Time, health float64) error
	SetStatus(ctx context.Context, id string, status domain.SourceStatus) error
}

// DegradeAfterFailures is the consecutive-failure count at which an
// active source is automatically demoted to degraded. Degraded sources
// still crawl (scheduleActive includes them) — the demotion exists so
// persistently-failing sources are visible in /admin/sources/health and
// the crawl.source.health metric instead of silently burning crawl
// budget at health 0 forever. A later success at recovered health
// promotes the source back to active.
const DegradeAfterFailures = 5

// PageCompletedHandler consumes crawl.page.completed.v1 and updates
// the sources row's health_score / consecutive_failures / needs_tuning
// / next_crawl_at fields. The logic mirrors the legacy inline updates
// in apps/crawler/cmd/main.go's processSource, split out so the crawl
// hot path is pure emit + the reconciliation runs on its own consumer
// group (its slowness never back-pressures fetches).
type PageCompletedHandler struct {
	repo SourceHealthRepo

	// EmitRegenerate, when non-nil, is invoked to trigger a recipe
	// regeneration for drift recovery. Only called for recipe-driven
	// sources (ExtractionRecipe set). nil disables the drift trigger
	// entirely (e.g. when RECIPE_ENABLED is off).
	EmitRegenerate func(ctx context.Context, sourceID, reason string)

	// RegenRejectRate / RegenMinPages gate the regenerate trigger
	// independently of the (hardcoded 0.8) needs_tuning flag. A
	// regenerate is emitted only when JobsFound >= RegenMinPages and
	// rejectRate >= RegenRejectRate. Zero RegenRejectRate disables it.
	RegenRejectRate float64
	RegenMinPages   int
}

// NewPageCompletedHandler wires the handler.
func NewPageCompletedHandler(repo SourceHealthRepo) *PageCompletedHandler {
	return &PageCompletedHandler{repo: repo}
}

// Name implements frame.EventI.
func (h *PageCompletedHandler) Name() string { return eventsv1.TopicCrawlPageCompleted }

// PayloadType returns a raw message — typed decode happens inside Execute.
func (h *PageCompletedHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

// Validate ensures the payload is non-empty.
func (h *PageCompletedHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("page-completed: empty payload")
	}
	return nil
}

// Execute applies success/failure bookkeeping and stamps next_crawl_at.
func (h *PageCompletedHandler) Execute(ctx context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil {
		return errors.New("page-completed: wrong payload type")
	}
	var env eventsv1.Envelope[eventsv1.CrawlPageCompletedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return fmt.Errorf("page-completed: decode: %w", err)
	}
	p := env.Payload

	log := util.Log(ctx).WithField("source_id", p.SourceID).WithField("request_id", p.RequestID)

	src, err := h.repo.GetByID(ctx, p.SourceID)
	if err != nil {
		return fmt.Errorf("page-completed: GetByID: %w", err)
	}
	if src == nil {
		log.Warn("page-completed: source vanished; dropping")
		return nil
	}

	now := time.Now().UTC()
	next := now.Add(time.Duration(src.CrawlIntervalSec) * time.Second)

	rejectRate := 0.0
	if p.JobsFound > 0 {
		rejectRate = float64(p.JobsRejected) / float64(p.JobsFound)
	}

	switch {
	case p.ErrorCode != "":
		// Connection/iterator failure — circuit-breaker style decay.
		telemetry.RecordSourceHealthEvent("failure")
		newHealth := src.HealthScore - 0.2
		if newHealth < 0 {
			newHealth = 0
		}
		consecutive := src.ConsecutiveFailures + 1
		if err := h.repo.RecordFailure(ctx, src.ID, newHealth, consecutive); err != nil {
			log.WithError(err).Warn("page-completed: RecordFailure failed")
		}
		// Persistent failure → demote to degraded so the source shows up
		// in health reporting instead of failing invisibly forever. Still
		// crawled (scheduleActive includes degraded); recovery is automatic
		// on the success path below.
		if consecutive >= DegradeAfterFailures && src.Status == domain.SourceActive {
			telemetry.RecordSourceHealthEvent("degraded")
			log.WithField("consecutive_failures", consecutive).
				Error("page-completed: source demoted to degraded after persistent failures")
			if err := h.repo.SetStatus(ctx, src.ID, domain.SourceDegraded); err != nil {
				log.WithError(err).Warn("page-completed: SetStatus(degraded) failed")
			}
		}
		// Phase 4 deliberately does not apply exponential backoff on
		// failure. Health-score decay (−0.2 above) is the gating signal;
		// the full per-topic drain-time policy lands in Phase 6 (design §8.3).
		if err := h.repo.UpdateNextCrawl(ctx, src.ID, next, now, newHealth); err != nil {
			log.WithError(err).Warn("page-completed: UpdateNextCrawl failed (err path)")
		}

	case rejectRate > 0.8 && p.JobsFound > 0:
		// High reject rate — flag for review but keep crawling.
		telemetry.RecordSourceHealthEvent("needs_tuning")
		if err := h.repo.FlagNeedsTuning(ctx, src.ID, true); err != nil {
			log.WithError(err).Warn("page-completed: FlagNeedsTuning failed")
		}
		if err := h.repo.UpdateNextCrawl(ctx, src.ID, next, now, src.HealthScore); err != nil {
			log.WithError(err).Warn("page-completed: UpdateNextCrawl failed (tuning path)")
		}

	default:
		newHealth := src.HealthScore + 0.1
		if newHealth > 1.0 {
			newHealth = 1.0
		}
		if err := h.repo.RecordSuccess(ctx, src.ID, newHealth); err != nil {
			log.WithError(err).Warn("page-completed: RecordSuccess failed")
		}
		if src.NeedsTuning && rejectRate < 0.5 {
			if err := h.repo.FlagNeedsTuning(ctx, src.ID, false); err != nil {
				log.WithError(err).Warn("page-completed: FlagNeedsTuning(clear) failed")
			}
		}
		// Auto-recover an auto-demoted source once health is back.
		if src.Status == domain.SourceDegraded && newHealth >= 0.8 {
			telemetry.RecordSourceHealthEvent("recovered")
			log.Info("page-completed: degraded source recovered; promoting to active")
			if err := h.repo.SetStatus(ctx, src.ID, domain.SourceActive); err != nil {
				log.WithError(err).Warn("page-completed: SetStatus(active) failed")
			}
		}
		if err := h.repo.UpdateNextCrawl(ctx, src.ID, next, now, newHealth); err != nil {
			log.WithError(err).Warn("page-completed: UpdateNextCrawl failed (success path)")
		}
	}

	// Drift recovery: independently of the needs_tuning flag (fixed 0.8),
	// emit a recipe regeneration once the configurable reject-rate drift
	// thresholds are crossed for a recipe-driven source. No-op when the
	// trigger is unwired or RegenRejectRate is zero (disabled).
	if h.EmitRegenerate != nil && h.RegenRejectRate > 0 && p.JobsFound >= h.RegenMinPages &&
		rejectRate >= h.RegenRejectRate && src.ExtractionRecipe != "" && src.ExtractionRecipe != "{}" {
		h.EmitRegenerate(ctx, src.ID, fmt.Sprintf("reject_rate=%.2f", rejectRate))
	}

	return nil
}
