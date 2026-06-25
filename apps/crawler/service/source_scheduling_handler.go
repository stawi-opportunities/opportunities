package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/pitabwire/util"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

// SourceSchedulingHandler consumes sources.scheduling.changed.v1 and drives the
// per-source Trustage crawl schedule to match the source's LIVE status: it
// loads the source and (a) archives the schedule when the source is gone, (b)
// ensures the schedule when the source is active/degraded, or (c) archives it
// otherwise. Re-deriving the desired state from the current status (rather than
// trusting the event to say "ensure" or "remove") makes the handler idempotent
// and order-independent — replays and out-of-order delivery converge.
type SourceSchedulingHandler struct {
	client       WorkflowClient
	getter       SourceGetter
	crawlBaseURL string
}

// NewSourceSchedulingHandler wires the handler from the Trustage WorkflowClient,
// a source getter, and the in-cluster crawl base URL the per-source schedules
// POST back to.
func NewSourceSchedulingHandler(client WorkflowClient, getter SourceGetter, crawlBaseURL string) *SourceSchedulingHandler {
	return &SourceSchedulingHandler{client: client, getter: getter, crawlBaseURL: crawlBaseURL}
}

// Name implements frame.EventI.
func (h *SourceSchedulingHandler) Name() string { return eventsv1.TopicSourceSchedulingChanged }

// PayloadType yields a raw message for typed decode inside Execute.
func (h *SourceSchedulingHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

// Validate ensures we have a parseable envelope.
func (h *SourceSchedulingHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("source-scheduling: empty payload")
	}
	return nil
}

// Execute decodes the envelope, loads the source, and reconciles its Trustage
// schedule against the source's live status.
func (h *SourceSchedulingHandler) Execute(ctx context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil {
		return errors.New("source-scheduling: wrong payload type")
	}
	var env eventsv1.Envelope[eventsv1.SourceSchedulingChangedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return fmt.Errorf("source-scheduling: decode: %w", err)
	}
	sourceID := env.Payload.SourceID
	if sourceID == "" {
		return errors.New("source-scheduling: missing source_id")
	}

	log := util.Log(ctx).WithField("source_id", sourceID)

	src, err := h.getter.GetByID(ctx, sourceID)
	if err != nil {
		return fmt.Errorf("source-scheduling: GetByID: %w", err)
	}

	// Re-derive from live status: gone or inactive → archive, active → ensure.
	if src == nil {
		if rerr := RemoveSourceSchedule(ctx, h.client, sourceID); rerr != nil {
			return fmt.Errorf("source-scheduling: remove (missing source): %w", rerr)
		}
		log.Debug("source-scheduling: source missing; schedule archived")
		return nil
	}
	if scheduleActive(src) {
		if eerr := EnsureSourceSchedule(ctx, h.client, src, h.crawlBaseURL); eerr != nil {
			return fmt.Errorf("source-scheduling: ensure: %w", eerr)
		}
		log.WithField("status", src.Status).Debug("source-scheduling: schedule ensured")
		return nil
	}
	if rerr := RemoveSourceSchedule(ctx, h.client, sourceID); rerr != nil {
		return fmt.Errorf("source-scheduling: remove (inactive): %w", rerr)
	}
	log.WithField("status", src.Status).Debug("source-scheduling: schedule archived")
	return nil
}
