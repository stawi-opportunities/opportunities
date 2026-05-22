package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/pitabwire/frame"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/publish"
	"github.com/stawi-opportunities/opportunities/pkg/variantstate"
)

// PublishHandler consumes CanonicalUpsertedV1 and writes a JSON
// snapshot to R2, then emits PublishedV1.
type PublishHandler struct {
	svc       *frame.Service
	publisher *publish.R2Publisher
	registry  *opportunity.Registry
	store     *variantstate.Store // nil-safe; soft-fails on Postgres outage
}

// NewPublishHandler ...
func NewPublishHandler(svc *frame.Service, p *publish.R2Publisher, reg *opportunity.Registry, store *variantstate.Store) *PublishHandler {
	return &PublishHandler{svc: svc, publisher: p, registry: reg, store: store}
}

// Name returns the topic this handler consumes (canonical upserts).
// It is not registered directly — the CanonicalFanout in service.go
// calls Execute on each sub-handler under one registry entry.
func (h *PublishHandler) Name() string { return eventsv1.TopicCanonicalsUpserted }

// PayloadType ...
func (h *PublishHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

// Validate ...
func (h *PublishHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("publish: empty payload")
	}
	return nil
}

// Execute writes the snapshot and emits PublishedV1.
func (h *PublishHandler) Execute(ctx context.Context, payload any) error {
	if h.publisher == nil {
		return nil // publisher not configured — skip
	}
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.CanonicalUpsertedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	c := env.Payload

	snap, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("publish: marshal: %w", err)
	}
	spec := h.registry.Resolve(c.Kind)
	key := publish.ObjectKey(spec.URLPrefix, c.Slug)
	if err := h.publisher.UploadPublicSnapshot(ctx, key, snap); err != nil {
		return fmt.Errorf("publish: upload: %w", err)
	}

	out := eventsv1.PublishedV1{
		OpportunityID: c.OpportunityID,
		Slug:          c.Slug,
		Kind:          c.Kind,
		R2Version:     int(time.Now().Unix()),
		PublishedAt:   time.Now().UTC(),
	}
	outEnv := eventsv1.NewEnvelope(eventsv1.TopicPublished, out)
	if err := h.svc.EventsManager().Emit(ctx, eventsv1.TopicPublished, outEnv); err != nil {
		return err
	}
	// Bulk-advance every variant in this canonical from `canonical`
	// → `published`. CanonicalUpsertedV1 is a many-to-one fan-in
	// (multiple variants share a canonical), so update by canonical_id.
	_ = h.store.MarkPublishedByCanonical(ctx, c.OpportunityID)
	return nil
}
