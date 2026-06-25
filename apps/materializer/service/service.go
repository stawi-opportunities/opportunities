// Package service drives the materializer as a Frame topic subscriber.
// Each Frame event (canonicals, canonicals_expired, translations,
// embeddings, source-stopped, auto-flagged) is decoded and written
// directly to the Postgres opportunities table via variantstate.Store —
// no Iceberg scan, no watermark polling, no leader election.
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
	"github.com/pitabwire/frame/queue"
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
	// Pipeline outputs moved to Frame Queue: EmbeddingV1 is consumed via a
	// dedicated queue subscriber (see EmbeddingWorker, wired in main.go);
	// CanonicalUpserted/Translation were materializer no-ops (the worker
	// upserts in-process) and are no longer on the events bus. The admin-
	// driven events below stay on the events bus (low volume, separate plane).
	mgr.Add(NewCanonicalExpiredHandler(s))
	mgr.Add(NewAutoFlaggedHandler(s))
	mgr.Add(NewSourceStoppedHandler(s))
	return nil
}

// EmbeddingWorker returns the embeddings Frame Queue subscriber. The caller
// registers it via frame.WithRegisterSubscriber in main.go.
func (s *Service) EmbeddingWorker() queue.SubscribeWorker {
	return NewEmbeddingHandler(s)
}

// (materializerIgnoreEvents removed — Frame v1.97.3 loose-mode handles
// the catch-all-stream pattern at the framework layer. See
// EventsManager().SetStrict(false) in RegisterSubscriptions above.)

// ---------------------------------------------------------------------------
// SourceStoppedHandler — TopicSourcesStopped
// ---------------------------------------------------------------------------

// SourceStoppedHandler hides every opportunity row carrying the matching
// source_id. Emitted by the crawler's /admin/sources/stop endpoint when
// an operator pulls the kill switch on a source — this is what makes
// historical jobs disappear from search.
//
// Implementation: variantstate.Store.HideBySource issues a single
// UPDATE that flips `deadline` into the past for every canonical with
// the given source_id. The API's search query filters by
// `deadline > now()` so the rows drop out of results immediately
// without loss of history.
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

// CanonicalUpsertHandler observes CanonicalUpsertedV1. The canonical
// row is already written by worker.CanonicalHandler in-process before
// the event is emitted (see worker/service/canonical.go), so the
// materializer's job here is to ack the event and let downstream
// side-effects hang off the same subscription later.
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

// TranslationHandler is a no-op against the opportunities table.
// Translated bodies live in R2 slug-direct (one object per
// canonical+lang) and are read by the public site/api at request time;
// the opportunities row carries only the source-language
// title/description.
//
// We keep the subscription so the Frame consumer-group acks the topic
// (no infinite redelivery), and so a future variant that surfaces
// per-lang title to the search path can be added without re-introducing
// the subscription.
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
	// No write — translations are served from R2 slug-direct.
	util.Log(ctx).
		WithField("opportunity_id", env.Payload.OpportunityID).
		WithField("lang", env.Payload.Lang).
		Debug("materializer: translation observed (R2-slug authoritative)")
	return nil
}

// ---------------------------------------------------------------------------
// EmbeddingHandler — TopicEmbeddings
// ---------------------------------------------------------------------------

// EmbeddingHandler updates the embedding column on the opportunities
// row for a given canonical.
type EmbeddingHandler struct{ s *Service }

func NewEmbeddingHandler(s *Service) *EmbeddingHandler {
	return &EmbeddingHandler{s: s}
}

var _ queue.SubscribeWorker = (*EmbeddingHandler)(nil)

// Handle consumes EmbeddingV1 from the embeddings Frame Queue and writes the
// vector to opportunities.embedding.
func (h *EmbeddingHandler) Handle(ctx context.Context, _ map[string]string, payload []byte) error {
	var env eventsv1.Envelope[eventsv1.EmbeddingV1]
	if err := json.Unmarshal(payload, &env); err != nil {
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
// "ignore" can be paired with a manual UPDATE to restore the row, but
// that's intentionally manual: an opportunity that tripped the user
// threshold should get human eyes before it surfaces again.
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
