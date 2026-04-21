package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/pitabwire/util"

	"stawi.jobs/pkg/domain"
	eventsv1 "stawi.jobs/pkg/events/v1"
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
}

// PageCompletedHandler consumes crawl.page.completed.v1 and updates
// the sources row's health_score / consecutive_failures / needs_tuning
// / next_crawl_at fields. The logic mirrors the legacy inline updates
// in apps/crawler/cmd/main.go's processSource, split out so the crawl
// hot path is pure emit + the reconciliation runs on its own consumer
// group (its slowness never back-pressures fetches).
type PageCompletedHandler struct {
	repo SourceHealthRepo
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
		newHealth := src.HealthScore - 0.2
		if newHealth < 0 {
			newHealth = 0
		}
		if err := h.repo.RecordFailure(ctx, src.ID, newHealth, src.ConsecutiveFailures+1); err != nil {
			log.WithError(err).Warn("page-completed: RecordFailure failed")
		}
		if err := h.repo.UpdateNextCrawl(ctx, src.ID, next, now, newHealth); err != nil {
			log.WithError(err).Warn("page-completed: UpdateNextCrawl failed (err path)")
		}

	case rejectRate > 0.8 && p.JobsFound > 0:
		// High reject rate — flag for review but keep crawling.
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
			_ = h.repo.FlagNeedsTuning(ctx, src.ID, false)
		}
		if err := h.repo.UpdateNextCrawl(ctx, src.ID, next, now, newHealth); err != nil {
			log.WithError(err).Warn("page-completed: UpdateNextCrawl failed (success path)")
		}
	}

	return nil
}
