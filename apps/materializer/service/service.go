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
// Manticore document map. Field names must match the idx_opportunities_rt schema.
//
// TODO(opportunity-generification): Phase 3.3 will rewrite the
// idx_opportunities_rt schema to surface kind + Attributes-driven
// facets. The mapping below extracts a few well-known string keys from
// Attributes so the materializer compiles against the new event shape.
func buildDocFromCanonical(p eventsv1.CanonicalUpsertedV1) map[string]any {
	desc, _ := p.Attributes["description"].(string)
	location, _ := p.Attributes["location_text"].(string)
	lang, _ := p.Attributes["language"].(string)
	remote, _ := p.Attributes["remote_type"].(string)
	employment, _ := p.Attributes["employment_type"].(string)
	seniority, _ := p.Attributes["seniority"].(string)
	category := ""
	if len(p.Categories) > 0 {
		category = p.Categories[0]
	}
	return map[string]any{
		"canonical_id":    p.OpportunityID,
		"slug":            p.Slug,
		"kind":            p.Kind,
		"title":           p.Title,
		"company":         p.IssuingEntity,
		"description":     desc,
		"location_text":   location,
		"category":        category,
		"country":         p.AnchorCountry,
		"language":        lang,
		"remote_type":     remote,
		"employment_type": employment,
		"seniority":       seniority,
		"salary_min":      uint64(p.AmountMin),
		"salary_max":      uint64(p.AmountMax),
		"currency":        p.Currency,
		"posted_at":       p.PostedAt.Unix(),
		"last_seen_at":    p.UpsertedAt.Unix(),
		"status":          "active",
	}
}

// ---------------------------------------------------------------------------
// CanonicalExpiredHandler — TopicCanonicalsExpired
// ---------------------------------------------------------------------------

// CanonicalExpiredHandler patches status='expired' + expires_at on the
// Manticore document when a canonical expires.
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
		"status":     "expired",
		"expires_at": env.Payload.ExpiredAt.Unix(),
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

// TranslationHandler records per-language translated text. It stores the
// translated body into a per-(canonical, lang) Manticore document keyed by
// hashID(canonical_id + ":" + lang). This is the first point where
// TopicTranslations reaches the search index.
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
	pl := env.Payload
	// Patch the title and description for the given language on the
	// per-(canonical,lang) document. The lang suffix in the key keeps
	// per-language rows independent so update is idempotent.
	doc := map[string]any{
		"canonical_id":  pl.OpportunityID,
		"lang":          pl.Lang,
		"title":         pl.TitleTr,
		"description":   pl.DescriptionTr,
		"model_version": pl.ModelVersion,
	}
	id := hashID(pl.OpportunityID + ":" + pl.Lang)
	if err := h.s.manticore.Replace(ctx, "idx_opportunities_rt", id, doc); err != nil {
		util.Log(ctx).WithError(err).
			WithField("opportunity_id", pl.OpportunityID).
			WithField("lang", pl.Lang).
			Error("materializer: translation replace failed")
		return fmt.Errorf("translation: replace: %w", err)
	}
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
	doc := map[string]any{
		"embedding":       pl.Vector,
		"embedding_model": pl.ModelVersion,
	}
	id := hashID(pl.OpportunityID)
	if err := h.s.manticore.Replace(ctx, "idx_opportunities_rt", id, doc); err != nil {
		util.Log(ctx).WithError(err).
			WithField("opportunity_id", pl.OpportunityID).
			Error("materializer: embedding replace failed")
		return fmt.Errorf("embedding: replace: %w", err)
	}
	return nil
}
