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
	"github.com/stawi-opportunities/opportunities/pkg/variantstate"
)

// Service is the materializer composition root.
//
// Post-consolidation the materializer is Postgres-only: every handler
// writes through the variantstate Store into the opportunities table.
// Worker.canonical writes the canonical row in-process, so the
// CanonicalUpsertHandler here is a no-op that just acks the
// CanonicalUpsertedV1 event. The remaining handlers (Embedding,
// SourceStopped, AutoFlagged, CanonicalExpired) push admin-state
// changes directly into opportunities.
type Service struct {
	svc   *frame.Service
	store *variantstate.Store
}

// NewService wires the Frame service and the opportunities-table store.
func NewService(svc *frame.Service, store *variantstate.Store) *Service {
	return &Service{svc: svc, store: store}
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
	// The materializer subscribes to the catch-all svc.opportunities.events.>
	// stream subject (same stream the worker + writer drain). Loose-mode
	// asks Frame to ack-and-skip any event whose Name() isn't on the
	// list below, instead of nack-storming on every sibling-consumer
	// event. The opt-in replaces the per-topic NoopHandler block that
	// used to live here — adding a new ignore-able topic now means
	// doing nothing, and adding a new INTERESTING topic means writing a
	// real handler and registering it here (the failure mode if you
	// forget is "ack-and-skip", which is debuggable; the old failure
	// mode if you forgot was "permanent NATS retry storm" which wedged
	// the consumer behind max_ack_pending).
	mgr.SetStrict(false)
	mgr.Add(NewCanonicalUpsertHandler(s))
	mgr.Add(NewCanonicalExpiredHandler(s))
	mgr.Add(NewTranslationHandler(s))
	mgr.Add(NewEmbeddingHandler(s))
	mgr.Add(NewAutoFlaggedHandler(s))
	mgr.Add(NewSourceStoppedHandler(s))
	return nil
}

// (materializerIgnoreEvents removed — Frame v1.97.3 loose-mode handles
// the catch-all-stream pattern at the framework layer. See
// EventsManager().SetStrict(false) in RegisterSubscriptions above.)

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
	// Postgres-only: hide every canonical with this source_id. Manticore
	// is being decommissioned; the API reads from opportunities directly.
	if err := h.s.store.HideBySource(ctx, env.Payload.SourceID, "source_stopped:"+env.Payload.Reason); err != nil {
		return fmt.Errorf("source-stopped: hide: %w", err)
	}
	util.Log(ctx).
		WithField("source_id", env.Payload.SourceID).
		WithField("reason", env.Payload.Reason).
		WithField("stopped_by", env.Payload.StoppedBy).
		Info("materializer: source stopped, opportunities hidden")
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
	// Worker.CanonicalHandler already UPSERTs opportunities in-process
	// before emitting CanonicalUpsertedV1 (see worker/service/canonical.go).
	// This handler stays subscribed so Frame loose-mode still has a
	// concrete handler to ack on, but does no work — the canonical row
	// is already in Postgres by the time we receive the event.
	//
	// Keeping the subscription avoids strict-mode redelivery storms if
	// SetStrict ever flips back on, and gives ops a clear place to add
	// downstream side-effects (notifications, web-hooks) later.
	_ = p
	_ = ctx
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
	if err := h.s.store.ExpireCanonical(ctx, env.Payload.OpportunityID, env.Payload.ExpiredAt); err != nil {
		return fmt.Errorf("canonical-expired: expire: %w", err)
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
	if err := h.s.store.UpdateEmbedding(ctx, pl.OpportunityID, pl.Vector); err != nil {
		return fmt.Errorf("embedding: update: %w", err)
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
		util.Log(ctx).
			WithField("slug", pl.Slug).
			WithField("flag_count", pl.FlagCount).
			Warn("materializer: auto-flag event missing opportunity_id; nothing to hide")
		return nil
	}
	if err := h.s.store.HideByCanonical(ctx, pl.OpportunityID, "auto_flagged"); err != nil {
		return fmt.Errorf("auto-flagged: hide: %w", err)
	}
	util.Log(ctx).
		WithField("opportunity_id", pl.OpportunityID).
		WithField("slug", pl.Slug).
		WithField("flag_count", pl.FlagCount).
		Warn("materializer: opportunity auto-flagged → hidden from search")
	return nil
}
