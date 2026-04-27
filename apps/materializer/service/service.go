// Package service drives the materializer as a Frame topic subscriber.
// Each Frame event (canonicals, canonicals_expired, translations, embeddings)
// is decoded and upserted to Manticore immediately — no Iceberg scan, no
// watermark polling, no leader election.
//
// Frame's NATS JetStream consumer group handles deduplication, redelivery,
// and consumer-lag observability natively (visible via `nats_jetstream_*`
// Prometheus metrics exported by the NATS server).
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/searchindex"
)

// Service is the materializer composition root.
type Service struct {
	svc       *frame.Service
	manticore *searchindex.Client
	bulkBatch int
}

// NewService wires the Frame service and Manticore client.
// bulkBatch is currently unused (handlers do single-doc replaces);
// it is retained so future buffered-handler variants can be added
// without a signature change.
func NewService(svc *frame.Service, mc *searchindex.Client, bulkBatch int) *Service {
	return &Service{svc: svc, manticore: mc, bulkBatch: bulkBatch}
}

// RegisterSubscriptions wires one handler per topic into the Frame
// events manager. Call this before svc.Run.
func (s *Service) RegisterSubscriptions() error {
	mgr := s.svc.EventsManager()
	if mgr == nil {
		return fmt.Errorf("materializer: events manager unavailable")
	}
	mgr.Add(NewCanonicalUpsertHandler(s))
	mgr.Add(NewCanonicalExpiredHandler(s))
	mgr.Add(NewTranslationHandler(s))
	mgr.Add(NewEmbeddingHandler(s))
	return nil
}

// ---------------------------------------------------------------------------
// CanonicalUpsertHandler — TopicCanonicalsUpserted
// ---------------------------------------------------------------------------

// CanonicalUpsertHandler decodes CanonicalUpsertedV1 and replaces the
// Manticore document keyed by hashID(canonical_id).
type CanonicalUpsertHandler struct{ s *Service }

func NewCanonicalUpsertHandler(s *Service) *CanonicalUpsertHandler {
	return &CanonicalUpsertHandler{s: s}
}

func (h *CanonicalUpsertHandler) Name() string { return eventsv1.TopicCanonicalsUpserted }

func (h *CanonicalUpsertHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

func (h *CanonicalUpsertHandler) Validate(_ context.Context, p any) error {
	r, ok := p.(*json.RawMessage)
	if !ok || r == nil || len(*r) == 0 {
		return errors.New("canonical-upsert: empty payload")
	}
	return nil
}

func (h *CanonicalUpsertHandler) Execute(ctx context.Context, p any) error {
	raw := p.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.CanonicalUpsertedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return fmt.Errorf("canonical-upsert: decode: %w", err)
	}
	doc := buildDocFromCanonical(env.Payload)
	id := hashID(env.Payload.OpportunityID)
	if err := h.s.manticore.Replace(ctx, "idx_opportunities_rt", id, doc); err != nil {
		util.Log(ctx).WithError(err).
			WithField("opportunity_id", env.Payload.OpportunityID).
			Error("materializer: canonical upsert failed")
		return fmt.Errorf("canonical-upsert: replace: %w", err)
	}
	return nil
}

// buildDocFromCanonical converts a CanonicalUpsertedV1 payload to a
// Manticore document map shaped for the polymorphic idx_opportunities_rt
// schema (kind + universal columns + per-kind sparse facet columns).
// Field names must match the DDL in pkg/searchindex/schema.go.
func buildDocFromCanonical(p eventsv1.CanonicalUpsertedV1) map[string]any {
	desc := attrString(p.Attributes, "description")

	doc := map[string]any{
		// Discriminator
		"kind": p.Kind,
		// Universal indexable text + categories
		"title":          p.Title,
		"description":    desc,
		"issuing_entity": p.IssuingEntity,
		"categories":     categoryIDs(p.Categories),
		// Universal location
		"country":   p.AnchorCountry,
		"region":    p.AnchorRegion,
		"city":      p.AnchorCity,
		"lat":       p.Lat,
		"lon":       p.Lon,
		"remote":    p.Remote,
		"geo_scope": p.GeoScope,
		// Universal time (unix seconds; timestamp columns)
		"posted_at": p.PostedAt.Unix(),
		"deadline":  deadlineUnix(p.Deadline),
		// Universal monetary
		"amount_min": p.AmountMin,
		"amount_max": p.AmountMax,
		"currency":   p.Currency,
	}
	// Per-kind sparse facet columns. Splice in only the columns the kind
	// owns; absent columns stay zero-valued in Manticore.
	cols, vals := sparseColsForKind(p.Kind, p.Attributes)
	for i, c := range cols {
		doc[c] = vals[i]
	}
	return doc
}

// deadlineUnix safely flattens an optional deadline pointer to a unix
// epoch second; nil → 0 (which Manticore treats as "no value").
func deadlineUnix(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return t.Unix()
}

// ---------------------------------------------------------------------------
// CanonicalExpiredHandler — TopicCanonicalsExpired
// ---------------------------------------------------------------------------

// CanonicalExpiredHandler marks an opportunity as expired by patching
// its `deadline` to the expiry instant. Search queries filter by
// `deadline > now()`, so a past deadline drops the row from results
// without losing it (history is still browseable by id). The polymorphic
// schema has no `status` column — `deadline` is the single source of
// truth for liveness.
type CanonicalExpiredHandler struct{ s *Service }

func NewCanonicalExpiredHandler(s *Service) *CanonicalExpiredHandler {
	return &CanonicalExpiredHandler{s: s}
}

func (h *CanonicalExpiredHandler) Name() string { return eventsv1.TopicCanonicalsExpired }

func (h *CanonicalExpiredHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

func (h *CanonicalExpiredHandler) Validate(_ context.Context, p any) error {
	r, ok := p.(*json.RawMessage)
	if !ok || r == nil || len(*r) == 0 {
		return errors.New("canonical-expired: empty payload")
	}
	return nil
}

func (h *CanonicalExpiredHandler) Execute(ctx context.Context, p any) error {
	raw := p.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.CanonicalExpiredV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return fmt.Errorf("canonical-expired: decode: %w", err)
	}
	doc := map[string]any{
		"deadline": env.Payload.ExpiredAt.Unix(),
	}
	id := hashID(env.Payload.OpportunityID)
	if err := h.s.manticore.Update(ctx, "idx_opportunities_rt", id, doc); err != nil {
		util.Log(ctx).WithError(err).
			WithField("opportunity_id", env.Payload.OpportunityID).
			Error("materializer: canonical expired patch failed")
		return fmt.Errorf("canonical-expired: update: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// TranslationHandler — TopicTranslations
// ---------------------------------------------------------------------------

// TranslationHandler is a no-op against Manticore for the polymorphic
// schema. Translated bodies live in R2 slug-direct (one object per
// canonical+lang) and are read by the public site/api at request time;
// the Manticore row carries only the source-language title/description.
//
// We keep the subscription so the Frame consumer-group acks the topic
// (no infinite redelivery), and so a future variant that surfaces
// per-lang title to BM25 can be added without re-introducing the
// subscription.
type TranslationHandler struct{ s *Service }

func NewTranslationHandler(s *Service) *TranslationHandler {
	return &TranslationHandler{s: s}
}

func (h *TranslationHandler) Name() string { return eventsv1.TopicTranslations }

func (h *TranslationHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

func (h *TranslationHandler) Validate(_ context.Context, p any) error {
	r, ok := p.(*json.RawMessage)
	if !ok || r == nil || len(*r) == 0 {
		return errors.New("translation: empty payload")
	}
	return nil
}

func (h *TranslationHandler) Execute(ctx context.Context, p any) error {
	raw := p.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.TranslationV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return fmt.Errorf("translation: decode: %w", err)
	}
	// No Manticore write — translations are served from R2 slug-direct.
	util.Log(ctx).
		WithField("opportunity_id", env.Payload.OpportunityID).
		WithField("lang", env.Payload.Lang).
		Debug("materializer: translation observed (R2-slug authoritative; no Manticore write)")
	return nil
}

// ---------------------------------------------------------------------------
// EmbeddingHandler — TopicEmbeddings
// ---------------------------------------------------------------------------

// EmbeddingHandler updates the embedding attribute on the Manticore
// document for a given canonical.
type EmbeddingHandler struct{ s *Service }

func NewEmbeddingHandler(s *Service) *EmbeddingHandler {
	return &EmbeddingHandler{s: s}
}

func (h *EmbeddingHandler) Name() string { return eventsv1.TopicEmbeddings }

func (h *EmbeddingHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

func (h *EmbeddingHandler) Validate(_ context.Context, p any) error {
	r, ok := p.(*json.RawMessage)
	if !ok || r == nil || len(*r) == 0 {
		return errors.New("embedding: empty payload")
	}
	return nil
}

func (h *EmbeddingHandler) Execute(ctx context.Context, p any) error {
	raw := p.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.EmbeddingV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return fmt.Errorf("embedding: decode: %w", err)
	}
	pl := env.Payload
	// /update patches the existing row in place. If the row hasn't been
	// upserted yet (embedding raced ahead of canonical), Manticore returns
	// updated=0 — the next CanonicalUpsertedV1 will re-build the doc and
	// the next embedding event for that id will re-apply.
	doc := map[string]any{
		"embedding": pl.Vector,
	}
	id := hashID(pl.OpportunityID)
	if err := h.s.manticore.Update(ctx, "idx_opportunities_rt", id, doc); err != nil {
		util.Log(ctx).WithError(err).
			WithField("opportunity_id", pl.OpportunityID).
			Error("materializer: embedding update failed")
		return fmt.Errorf("embedding: update: %w", err)
	}
	return nil
}
