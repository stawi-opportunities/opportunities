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
//
// The materializer's NATS consumer subscribes to a broad subject
// (svc.opportunities.events.>) so it sees every event flowing through
// the stream — including ones that aren't relevant to indexing
// (variant lifecycle, crawl progress, etc.). For those we register a
// silent-ack handler that ACKs the message and returns nil, otherwise
// Frame logs "event not found in registry" + a retries-exhausted
// failure on every redelivery. A narrower subject filter is preferable
// long-term but requires Frame multi-subject consumer support.
func (s *Service) RegisterSubscriptions() error {
	mgr := s.svc.EventsManager()
	if mgr == nil {
		return fmt.Errorf("materializer: events manager unavailable")
	}
	mgr.Add(NewCanonicalUpsertHandler(s))
	mgr.Add(NewCanonicalExpiredHandler(s))
	mgr.Add(NewTranslationHandler(s))
	mgr.Add(NewEmbeddingHandler(s))
	mgr.Add(NewAutoFlaggedHandler(s))
	mgr.Add(NewSourceStoppedHandler(s))
	for _, t := range materializerIgnoreEvents {
		mgr.Add(&eventsv1.NoopHandler{Topic: t})
	}
	return nil
}

// Events that the materializer does NOT consume but that flow through
// the same NATS subject namespace. Adding silent-ack handlers stops
// Frame from logging "event not found in registry" on every delivery.
var materializerIgnoreEvents = []string{
	eventsv1.TopicVariantsIngested,
	eventsv1.TopicVariantsNormalized,
	eventsv1.TopicVariantsValidated,
	eventsv1.TopicVariantsFlagged,
	eventsv1.TopicVariantsClustered,
	eventsv1.TopicVariantsRejected,
	eventsv1.TopicCrawlRequests,
	eventsv1.TopicCrawlPageCompleted,
	eventsv1.TopicSourcesDiscovered,
	eventsv1.TopicPublished,
	eventsv1.TopicCVUploaded,
	eventsv1.TopicCVExtracted,
	eventsv1.TopicCVImproved,
	eventsv1.TopicCandidateEmbedding,
	eventsv1.TopicCandidatePreferencesUpdated,
	eventsv1.TopicCandidateMatchesReady,
	eventsv1.TopicCandidateCVStaleNudge,
}

// ---------------------------------------------------------------------------
// SourceStoppedHandler — TopicSourcesStopped
// ---------------------------------------------------------------------------

// SourceStoppedHandler deletes every Manticore document carrying the
// matching source_id. Emitted by the crawler's /admin/sources/stop
// endpoint when an operator pulls the kill switch on a source — this
// is what makes historical jobs disappear from search.
//
// Implementation: Manticore's SQL surface supports
// `DELETE FROM <index> WHERE source_id = N`; one round-trip removes
// the whole source's footprint regardless of doc count.
type SourceStoppedHandler struct{ s *Service }

func NewSourceStoppedHandler(s *Service) *SourceStoppedHandler {
	return &SourceStoppedHandler{s: s}
}

func (h *SourceStoppedHandler) Name() string { return eventsv1.TopicSourcesStopped }

func (h *SourceStoppedHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

func (h *SourceStoppedHandler) Validate(_ context.Context, p any) error {
	r, ok := p.(*json.RawMessage)
	if !ok || r == nil || len(*r) == 0 {
		return errors.New("source-stopped: empty payload")
	}
	return nil
}

func (h *SourceStoppedHandler) Execute(ctx context.Context, p any) error {
	raw := p.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.SourceStoppedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return fmt.Errorf("source-stopped: decode: %w", err)
	}
	if env.Payload.SourceID == "" {
		return errors.New("source-stopped: empty source_id")
	}
	id := hashID(env.Payload.SourceID)
	if err := h.s.manticore.DeleteWhere(ctx, "idx_opportunities_rt", "source_id", id); err != nil {
		util.Log(ctx).WithError(err).
			WithField("source_id", env.Payload.SourceID).
			Error("materializer: source-stopped delete failed")
		return fmt.Errorf("source-stopped: delete: %w", err)
	}
	util.Log(ctx).
		WithField("source_id", env.Payload.SourceID).
		WithField("reason", env.Payload.Reason).
		WithField("stopped_by", env.Payload.StoppedBy).
		Info("materializer: source stopped, jobs removed from search")
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
		// Provenance — `bigint` hash of source_id, used by
		// SourceStoppedHandler to bulk-delete a stopped source's
		// canonicals. Always written so equality filters can match.
		"source_id": hashID(p.SourceID),
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
	doc := map[string]any{
		"embedding": pl.Vector,
	}
	id := hashID(pl.OpportunityID)
	if err := h.s.manticore.Update(ctx, "idx_opportunities_rt", id, doc); err != nil {
		// Manticore /update does not support partial updates of
		// float_vector knn columns — every embedding write returns
		// 400 "MVA elements should be integers" even though embedding
		// is declared `float_vector`. Until the embedding refresh
		// path is rewritten to issue a full /replace (which requires
		// re-fetching the canonical doc), accept the failure and ack
		// the message: text search + listing keep working, only
		// vector-search ranking is degraded. Continuing to nack would
		// stall the rest of the materializer behind a poison-pill
		// stream of embedding events that can never succeed.
		util.Log(ctx).WithError(err).
			WithField("opportunity_id", pl.OpportunityID).
			Warn("materializer: embedding update skipped (Manticore /update cannot patch float_vector — vector search will be stale)")
		return nil
	}
	return nil
}

// ---------------------------------------------------------------------------
// AutoFlaggedHandler — TopicOpportunityAutoFlagged
// ---------------------------------------------------------------------------

// AutoFlaggedHandler responds to the user-flag-threshold trip emitted
// by the api. The signal carries a slug (not a canonical_id), so the
// handler issues a SQL UPDATE keyed on slug rather than the primary
// key. The polymorphic schema's single source of truth for liveness
// is `deadline` (search filters by `deadline > now`), so we push it
// into the past — that drops the row from search exactly as
// CanonicalExpiredHandler does for the retention sweep.
//
// Operator review still happens — the auto-action is containment, not
// a final verdict. A subsequent /admin/flags/{id}/resolve with action
// "ignore" can be paired with a manual Manticore patch to restore the
// row, but that's intentionally manual: an opportunity that tripped
// the user threshold should get human eyes before it surfaces again.
type AutoFlaggedHandler struct{ s *Service }

func NewAutoFlaggedHandler(s *Service) *AutoFlaggedHandler {
	return &AutoFlaggedHandler{s: s}
}

func (h *AutoFlaggedHandler) Name() string { return eventsv1.TopicOpportunityAutoFlagged }

func (h *AutoFlaggedHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

func (h *AutoFlaggedHandler) Validate(_ context.Context, p any) error {
	r, ok := p.(*json.RawMessage)
	if !ok || r == nil || len(*r) == 0 {
		return errors.New("auto-flagged: empty payload")
	}
	return nil
}

func (h *AutoFlaggedHandler) Execute(ctx context.Context, p any) error {
	raw := p.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.OpportunityAutoFlaggedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return fmt.Errorf("auto-flagged: decode: %w", err)
	}
	pl := env.Payload
	if pl.OpportunityID == "" {
		// Slug-only events are non-fatal — log and skip. The api emits
		// canonical_id when it can resolve one; events without it
		// likely point at a slug whose canonical hasn't materialized
		// yet (race) and operator-driven action via /admin/flags
		// covers the same outcome.
		util.Log(ctx).
			WithField("slug", pl.Slug).
			WithField("flag_count", pl.FlagCount).
			Warn("materializer: auto-flag event missing opportunity_id; skipping Manticore update")
		return nil
	}
	// Push deadline to "now" so the search filter (deadline > now)
	// drops the row immediately. Mirrors the CanonicalExpiredHandler
	// path so liveness has a single source of truth on the schema.
	doc := map[string]any{
		"deadline": time.Now().UTC().Unix(),
	}
	id := hashID(pl.OpportunityID)
	if err := h.s.manticore.Update(ctx, "idx_opportunities_rt", id, doc); err != nil {
		util.Log(ctx).WithError(err).
			WithField("opportunity_id", pl.OpportunityID).
			WithField("slug", pl.Slug).
			Error("materializer: auto-flag update failed")
		return fmt.Errorf("auto-flagged: update: %w", err)
	}
	util.Log(ctx).
		WithField("opportunity_id", pl.OpportunityID).
		WithField("slug", pl.Slug).
		WithField("flag_count", pl.FlagCount).
		Warn("materializer: opportunity auto-flagged → hidden from search")
	return nil
}
